//go:build with_gvisor && linux

package iwan

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"golang.org/x/sys/unix"
)

func TestRawSockaddrAddrPortRoundTrip(t *testing.T) {
	t.Parallel()
	remote := netip.MustParseAddrPort("127.0.0.1:4567")
	var raw unix.RawSockaddrAny
	nameLength, ok := fillRawSockaddrAddrPort(&raw, remote)
	if !ok {
		t.Fatal("failed to fill raw sockaddr")
	}
	if nameLength != unix.SizeofSockaddrInet4 {
		t.Fatalf("unexpected sockaddr length: %d", nameLength)
	}
	parsed, ok := rawSockaddrAddrPort(&raw)
	if !ok {
		t.Fatal("failed to parse raw sockaddr")
	}
	if parsed != remote {
		t.Fatalf("unexpected address: %v", parsed)
	}
}

func TestWriteDataPacketVectorBatchTo(t *testing.T) {
	t.Parallel()
	receiver, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	remote := M.SocksaddrFromNet(receiver.LocalAddr()).Unwrap().AddrPort()
	token := [2]byte{0x12, 0x34}
	sessionID := [4]byte{0xde, 0xad, 0xbe, 0xef}
	server := &Server{}
	packets := []serverOutboundPacket{
		{remote: remote, token: token, sessionID: sessionID, payload: []byte("first")},
		{remote: remote, token: token, sessionID: sessionID, payload: []byte("second")},
		{remote: remote, token: token, sessionID: sessionID, views: [][]byte{[]byte("third-"), []byte("views")}, size: len("third-views")},
		{remote: remote, token: token, sessionID: sessionID},
	}
	sent, err := server.writeDataPacketVectorBatchTo(sender, packets)
	if err != nil {
		t.Fatal(err)
	}
	if sent != len(packets) {
		t.Fatalf("unexpected sent count: %d", sent)
	}
	for index, expected := range packets {
		expectedPayload := expected.payload
		if len(expected.views) > 0 {
			expectedPayload = nil
			for _, view := range expected.views {
				expectedPayload = append(expectedPayload, view...)
			}
		}
		_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
		buffer := make([]byte, headerSize+len(expectedPayload))
		n, _, err := receiver.ReadFromUDPAddrPort(buffer)
		if err != nil {
			t.Fatal(err)
		}
		packet := buffer[:n]
		if packet[0] != packetData || packet[1] != 0 || string(packet[2:4]) != string(token[:]) || string(packet[4:8]) != string(sessionID[:]) {
			t.Fatalf("packet %d has invalid header: %x", index, packet[:headerSize])
		}
		if string(packet[headerSize:]) != string(expectedPayload) {
			t.Fatalf("packet %d payload = %q, want %q", index, packet[headerSize:], expectedPayload)
		}
	}
}

func TestSendmmsgErrorReturnsZeroSent(t *testing.T) {
	t.Parallel()
	sent, err := sendmmsg(-1, []mmsghdr{{}})
	if !errors.Is(err, unix.EBADF) {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}
}
