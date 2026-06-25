package urltest

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type tcpTestDialer struct {
	network     string
	destination M.Socksaddr
	err         error
}

func (d *tcpTestDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.network = network
	d.destination = destination
	if d.err != nil {
		return nil, d.err
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (d *tcpTestDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func TestURLTestSupportsTCPURL(t *testing.T) {
	t.Parallel()

	dialer := new(tcpTestDialer)
	_, err := URLTest(context.Background(), "tcp://example.com:443", dialer)
	if err != nil {
		t.Fatal(err)
	}
	if dialer.network != N.NetworkTCP {
		t.Fatalf("expected tcp network, got %s", dialer.network)
	}
	if dialer.destination.String() != "example.com:443" {
		t.Fatalf("expected example.com:443, got %s", dialer.destination)
	}
}

func TestTCPTestRequiresPort(t *testing.T) {
	t.Parallel()

	linkURL, err := url.Parse("tcp://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tcpTest(context.Background(), linkURL, new(tcpTestDialer)); err == nil {
		t.Fatal("expected missing port error")
	}
}

func TestTCPTestReturnsDialError(t *testing.T) {
	t.Parallel()

	linkURL, err := url.Parse("tcp://example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	expectedErr := errors.New("dial failed")
	_, err = tcpTest(context.Background(), linkURL, &tcpTestDialer{err: expectedErr})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected dial error, got %v", err)
	}
}
