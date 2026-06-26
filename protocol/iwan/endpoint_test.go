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
	defer endpoint.Close()

	if err = endpoint.Start(adapter.StartStateStart); err != nil {
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
