//go:build with_gvisor

package iwan

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

func TestEndpointRejectsUsernameThatOverflowsOpenTLV(t *testing.T) {
	t.Parallel()
	username := strings.Repeat("a", 254)

	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  t.Context(),
		Logger:   logger.NOP(),
		Dialer:   &pipeDialer{servers: make(chan net.Conn, 1)},
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", defaultPort),
		Username: username,
		Password: "mypassword",
	})
	if err == nil {
		_ = endpoint.Close()
		t.Fatal("endpoint accepted username that overflows OPEN TLV")
	}
	if !errors.Is(err, errUsernameTooLong) {
		t.Fatalf("unexpected endpoint username error: %v", err)
	}
}

func TestServerRejectsUsernameThatOverflowsOpenTLV(t *testing.T) {
	t.Parallel()
	username := strings.Repeat("a", 254)

	server, err := NewServer(ServerOptions{
		Context:     t.Context(),
		Logger:      logger.NOP(),
		AddressPool: netip.MustParsePrefix("10.66.0.0/24"),
		Users:       []ServerUser{{Username: username, Password: "mypassword"}},
	})
	if err == nil {
		_ = server.Close()
		t.Fatal("server accepted username that overflows OPEN TLV")
	}
	if !errors.Is(err, errUsernameTooLong) {
		t.Fatalf("unexpected server username error: %v", err)
	}
}

func TestServerAuthenticatesMaximumLengthUsername(t *testing.T) {
	t.Parallel()
	username := strings.Repeat("a", 253)
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{{Username: username, Password: "mypassword"}})
	defer cleanup()
	openPacket, err := buildOpenPacket(username, "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("server rejected maximum-length authenticated username")
	}
	server.access.Lock()
	session := server.byUsername[username]
	server.access.Unlock()
	if session == nil {
		t.Fatal("maximum-length username did not create a session")
	}
}
