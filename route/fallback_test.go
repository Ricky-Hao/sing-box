package route

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type fallbackRouteTestOutbound struct {
	outbound.Adapter
	err         error
	dialCount   atomic.Int32
	listenCount atomic.Int32
}

func (o *fallbackRouteTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	return nil, o.err
}

func (o *fallbackRouteTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	o.listenCount.Add(1)
	return nil, o.err
}

type fallbackRouteTestManager struct {
	adapter.OutboundManager
	outbounds       map[string]adapter.Outbound
	defaultOutbound adapter.Outbound
}

func (m *fallbackRouteTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	detour, loaded := m.outbounds[tag]
	return detour, loaded
}

func (m *fallbackRouteTestManager) Default() adapter.Outbound {
	return m.defaultOutbound
}

func newFallbackRouteTestRouter(t *testing.T, nested bool, interruptConnections bool) (*Router, *group.Fallback, *fallbackRouteTestOutbound, *fallbackRouteTestOutbound) {
	t.Helper()
	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(t.Context(), history)
	manager := &fallbackRouteTestManager{outbounds: make(map[string]adapter.Outbound)}
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	logger := log.NewNOPFactory().Logger()
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	newLeaf := func(tag string) *fallbackRouteTestOutbound {
		leaf := &fallbackRouteTestOutbound{
			Adapter: outbound.NewAdapter("test", tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
			err:     errors.New(tag + " unavailable"),
		}
		manager.outbounds[tag] = leaf
		history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
		return leaf
	}
	primary := newLeaf("primary")
	secondary := newLeaf("secondary")
	detour, err := group.NewFallback(ctx, nil, logger, "fallback", option.FallbackOutboundOptions{
		Outbounds:                 []string{primary.Tag(), secondary.Tag()},
		InterruptExistConnections: interruptConnections,
	})
	if err != nil {
		t.Fatal(err)
	}
	fallback := detour.(*group.Fallback)
	manager.outbounds[fallback.Tag()] = fallback
	if err = scope.Start("fallback", fallback, adapter.StartStateInitialize); err != nil {
		t.Fatal(err)
	}
	if err = scope.Start("fallback", fallback, adapter.StartStateStart); err != nil {
		t.Fatal(err)
	}
	fallback.PerformUpdateCheck()
	manager.defaultOutbound = fallback
	if nested {
		selector, createErr := group.NewSelector(ctx, nil, logger, "selector", option.SelectorOutboundOptions{
			Outbounds: []string{fallback.Tag()},
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if err = scope.Start("selector", selector.(adapter.Lifecycle), adapter.StartStateStart); err != nil {
			t.Fatal(err)
		}
		manager.defaultOutbound = selector
	}
	return &Router{
		ctx:        ctx,
		logger:     logger,
		outbound:   manager,
		connection: NewConnectionManager(ctx, logger),
	}, fallback, primary, secondary
}

func TestRoutedFallbackFailureSwitchesNextRequest(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "fallback"
		if nested {
			name = "selector/fallback"
		}
		for _, network := range []string{N.NetworkTCP, N.NetworkUDP, "udp-connect"} {
			t.Run(name+"/"+network, func(t *testing.T) {
				t.Parallel()
				router, fallback, primary, secondary := newFallbackRouteTestRouter(t, nested, false)
				request := func() error {
					metadata := adapter.InboundContext{
						Destination: M.ParseSocksaddr("example.invalid:80"),
						Domain:      "example.invalid",
						UDPConnect:  network == "udp-connect",
					}
					closed := make(chan error, 1)
					onClose := func(err error) { closed <- err }
					if network == N.NetworkTCP {
						client, server := net.Pipe()
						defer client.Close()
						defer server.Close()
						router.RouteConnectionEx(t.Context(), client, metadata, onClose)
					} else {
						conn, err := net.ListenPacket("udp", "127.0.0.1:0")
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						router.RoutePacketConnectionEx(t.Context(), bufio.NewPacketConn(conn), metadata, onClose)
					}
					select {
					case err := <-closed:
						return err
					case <-time.After(time.Second):
						t.Fatal("routed request did not report its failure")
						return nil
					}
				}
				if err := request(); !errors.Is(err, primary.err) {
					t.Fatalf("expected primary failure without a retry, got %v", err)
				}
				if fallback.Selected(N.NetworkTCP) != secondary || fallback.Selected(N.NetworkUDP) != secondary {
					t.Fatal("routed leaf failure did not update fallback selection")
				}
				if references := fallback.References(); !slices.Equal(references, []string{secondary.Tag()}) {
					t.Fatalf("expected secondary reference after switching, got %v", references)
				}
				if err := request(); !errors.Is(err, secondary.err) {
					t.Fatalf("expected next request to use secondary, got %v", err)
				}
				for _, leaf := range []*fallbackRouteTestOutbound{primary, secondary} {
					if calls := leaf.dialCount.Load() + leaf.listenCount.Load(); calls != 1 {
						t.Fatalf("expected exactly one call to %s, got %d", leaf.Tag(), calls)
					}
				}
			})
		}
	}
}

type fallbackRouteTestCloser struct {
	closed int
}

func (c *fallbackRouteTestCloser) Close() error {
	c.closed++
	return nil
}

func TestRoutedFallbackInterruptAndDetach(t *testing.T) {
	for _, interruptConnections := range []bool{false, true} {
		name := "preserve-external"
		if interruptConnections {
			name = "interrupt-external"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router, fallback, primary, secondary := newFallbackRouteTestRouter(t, true, interruptConnections)
			chain, err := resolveOutbound(router.outbound.Default(), N.NetworkTCP)
			if err != nil {
				t.Fatal(err)
			}
			closer := new(fallbackRouteTestCloser)
			onClose := registerInterrupt(chain, closer, nil)
			router.connection.(*ConnectionManager).deleteURLTestHistory(chain)
			if interruptConnections && closer.closed != 1 || !interruptConnections && closer.closed != 0 {
				t.Fatalf("unexpected close count on switch: %d", closer.closed)
			}
			onClose(nil)
			before := closer.closed
			history := service.PtrFromContext[urltest.HistoryStorage](router.ctx)
			history.StoreURLTestHistory(primary.Tag(), &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
			history.DeleteURLTestHistory(secondary.Tag())
			fallback.PerformUpdateCheck()
			if fallback.Selected(N.NetworkTCP) != primary {
				t.Fatal("expected failback after secondary becomes unavailable")
			}
			if closer.closed != before {
				t.Fatal("detached connection was interrupted again")
			}
		})
	}
}

var (
	_ adapter.Outbound        = (*fallbackRouteTestOutbound)(nil)
	_ adapter.OutboundManager = (*fallbackRouteTestManager)(nil)
)
