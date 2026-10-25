//go:build with_iwan && with_gvisor

package iwan

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	iwanTransport "github.com/sagernet/sing-box/transport/iwan"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

func TestEndpointStartStageDefersTransportDialUntilReadyWait(t *testing.T) {
	t.Parallel()

	dialer := &startStagePipeDialer{
		servers: make(chan net.Conn, 1),
	}
	transportEndpoint, err := iwanTransport.NewEndpoint(iwanTransport.EndpointOptions{
		Context:  t.Context(),
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", 4567),
		MTU:      1400,
		Username: "myuser",
		Password: "mypassword",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &Endpoint{
		endpoint: transportEndpoint,
	}
	scope := adapter.NewScope(t.Context(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	if err = endpoint.Start(adapter.StartStateInitialize, scope); err != nil {
		t.Fatal(err)
	}

	if err = endpoint.Start(adapter.StartStateStart, scope); err != nil {
		t.Fatal(err)
	}
	select {
	case server := <-dialer.servers:
		_ = server.Close()
		t.Fatal("transport dialed before DNS/router startup could complete")
	default:
	}

	waitCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = endpoint.waitReady(waitCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected waitReady deadline, got %v", err)
	}
	select {
	case server := <-dialer.servers:
		_ = server.Close()
	default:
		t.Fatal("waitReady did not start transport")
	}
}

type startStagePipeDialer struct {
	servers chan net.Conn
}

func (d *startStagePipeDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	d.servers <- server
	return client, nil
}

func (d *startStagePipeDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (d *startStagePipeDialer) Upstream() any {
	return nil
}

func (d *startStagePipeDialer) Start() error {
	return nil
}

func (d *startStagePipeDialer) Close() error {
	return nil
}

func (d *startStagePipeDialer) InterfaceUpdated() {
}

func (d *startStagePipeDialer) Addr() netip.Addr {
	return netip.Addr{}
}

func TestEndpointScopeClosesTransport(t *testing.T) {
	t.Parallel()

	dialer := &startStagePipeDialer{servers: make(chan net.Conn, 1)}
	transportEndpoint, err := iwanTransport.NewEndpoint(iwanTransport.EndpointOptions{
		Context:  t.Context(),
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddr("127.0.0.1:4567"),
		MTU:      1280,
		Username: "test-user",
		Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &Endpoint{endpoint: transportEndpoint}
	scope := adapter.NewScope(t.Context(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range []adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart, adapter.StartStatePostStart} {
		if err = scope.Start("iwan", endpoint, stage); err != nil {
			t.Fatal(err)
		}
	}
	server := <-dialer.servers
	defer server.Close()
	if err = server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = scope.Close(); err != nil {
		t.Fatal(err)
	}
	if endpoint.started.Load() || endpoint.endpointStarted.Load() || transportEndpoint.Ready() {
		t.Fatal("scope close left endpoint started or ready")
	}
	var buffer [1]byte
	if _, err = server.Read(buffer[:]); err == nil {
		t.Fatal("scope did not close transport connection")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("transport connection remained open after scope close")
	}
}

type failedStartDialer struct {
	startStagePipeDialer
	ctx context.Context
	err error
}

func (d *failedStartDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.ctx = ctx
	return nil, d.err
}

func TestEndpointScopeCleansUpFailedStart(t *testing.T) {
	t.Parallel()

	dialer := &failedStartDialer{err: errors.New("dial failed")}
	transportEndpoint, err := iwanTransport.NewEndpoint(iwanTransport.EndpointOptions{
		Context:  t.Context(),
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddr("127.0.0.1:4567"),
		MTU:      1280,
		Username: "test-user",
		Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &Endpoint{endpoint: transportEndpoint}
	scope := adapter.NewScope(t.Context(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range []adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart} {
		if err = scope.Start("iwan", endpoint, stage); err != nil {
			t.Fatal(err)
		}
	}
	if err = scope.Start("iwan", endpoint, adapter.StartStatePostStart); !errors.Is(err, dialer.err) {
		t.Fatalf("expected transport start failure, got %v", err)
	}
	if err = scope.Close(); err != nil {
		t.Fatal(err)
	}
	if endpoint.started.Load() || endpoint.endpointStarted.Load() || !errors.Is(dialer.ctx.Err(), context.Canceled) {
		t.Fatal("failed-start endpoint was not cleaned up by scope")
	}
}
