//go:build with_iwan && with_gvisor

package iwan

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	iwanTransport "github.com/sagernet/sing-box/transport/iwan"
	"github.com/sagernet/sing/common/json/badoption"
)

func TestInboundScopeClosesListener(t *testing.T) {
	t.Parallel()

	loopback := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	logger := log.NewNOPFactory().Logger()
	server, err := iwanTransport.NewServer(iwanTransport.ServerOptions{
		Context:     t.Context(),
		Logger:      logger,
		AddressPool: netip.MustParsePrefix("10.0.0.0/24"),
		Users:       []iwanTransport.ServerUser{{Username: "test-user", Password: "test-password"}},
		MTU:         1280,
	})
	if err != nil {
		t.Fatal(err)
	}
	inbound := &Inbound{
		server: server,
		listener: listener.New(listener.Options{
			Context: t.Context(),
			Logger:  logger,
			Listen:  option.ListenOptions{Listen: &loopback, UDPFragmentDefault: true},
		}),
	}
	scope := adapter.NewScope(t.Context(), logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range []adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart} {
		if err = scope.Start("iwan", inbound, stage); err != nil {
			t.Fatal(err)
		}
	}
	conn := inbound.listener.UDPConn()
	if err = scope.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.WriteTo([]byte{0}, conn.LocalAddr()); err == nil {
		t.Fatal("scope did not close inbound UDP socket")
	}
}
