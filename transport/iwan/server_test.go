//go:build with_gvisor

package iwan

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/gvisor/pkg/tcpip/header"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestServerEndpointHandshake(t *testing.T) {
	t.Parallel()
	server, endpoint, cleanup := startTestServerAndEndpoint(t, true)
	defer cleanup()
	waitFor(t, endpoint.Ready)
	addresses := endpoint.LocalAddresses()
	if len(addresses) != 1 {
		t.Fatalf("expected one endpoint address, got %v", addresses)
	}
	if addresses[0] != netip.MustParsePrefix("10.66.0.2/24") {
		t.Fatalf("unexpected endpoint address: %v", addresses[0])
	}
	if user := server.UserByAddress(netip.MustParseAddr("10.66.0.2")); user != "myuser" {
		t.Fatalf("unexpected session user: %q", user)
	}
}

func TestServerRejectsSpoofedInnerSource(t *testing.T) {
	t.Parallel()
	server, endpoint, cleanup := startTestServerAndEndpoint(t, true)
	defer cleanup()
	waitFor(t, endpoint.Ready)
	server.access.Lock()
	var session *serverSession
	for _, candidate := range server.byRemote {
		session = candidate
		break
	}
	server.access.Unlock()
	if session == nil {
		t.Fatal("missing server session")
	}
	packet := wrapDataPacket(session, buildIPv4Packet(netip.MustParseAddr("10.66.0.99"), netip.MustParseAddr("1.1.1.1")))
	if server.handleData(session.remote, packet) {
		t.Fatal("accepted DATA with spoofed inner source")
	}
}

func TestServerAuthenticatedOpenTakesOverUsernameFromNewSource(t *testing.T) {
	t.Parallel()
	server, endpoint, cleanup := startTestServerAndEndpoint(t, false)
	defer cleanup()
	waitFor(t, endpoint.Ready)
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	server.access.Lock()
	conn := server.conn
	server.access.Unlock()
	endpoint.access.Lock()
	oldRemote := M.SocksaddrFromNet(endpoint.conn.LocalAddr()).Unwrap().AddrPort()
	endpoint.access.Unlock()
	remote := M.SocksaddrFromNet(clientConn.LocalAddr()).Unwrap().AddrPort()
	if !server.handleOpen(conn, remote, openPacket) {
		t.Fatal("authenticated OPEN takeover failed")
	}
	server.access.Lock()
	defer server.access.Unlock()
	session := server.byUsername["myuser"]
	if session == nil || session.remote != remote {
		t.Fatalf("session did not move to new remote: %+v", session)
	}
	if oldSession := server.byRemote[oldRemote]; oldSession != nil {
		t.Fatal("old remote still maps to session")
	}
}

func TestServerRejectsSameSourceDifferentUserWithoutCorruption(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{
		{Username: "myuser", Password: "mypassword"},
		{Username: "other", Password: "otherpassword"},
	})
	defer cleanup()
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("initial OPEN failed")
	}
	otherOpenPacket, err := buildOpenPacket("other", "otherpassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if server.handleOpen(packetConn, remote, otherOpenPacket) {
		t.Fatal("accepted different user from existing remote")
	}
	server.access.Lock()
	defer server.access.Unlock()
	if session := server.byRemote[remote]; session == nil || session.username != "myuser" {
		t.Fatalf("remote session was corrupted: %+v", session)
	}
	if server.byUsername["myuser"] == nil {
		t.Fatal("original username session was removed")
	}
	if server.byUsername["other"] != nil {
		t.Fatal("different username session was added")
	}
}

func TestServerRejectsInvalidAuthenticationWithoutSession(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{{Username: "myuser", Password: "mypassword"}})
	defer cleanup()
	openPacket, err := buildOpenPacket("myuser", "wrong-password", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("accepted invalid credentials")
	}
	server.access.Lock()
	defer server.access.Unlock()
	if len(server.byRemote) != 0 || len(server.byAddress) != 0 || len(server.byUsername) != 0 {
		t.Fatal("authentication failure created session state")
	}
}

func TestServerRejectsWrongTokenAndSession(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{{Username: "myuser", Password: "mypassword"}})
	defer cleanup()
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("OPEN failed")
	}
	server.access.Lock()
	session := server.byRemote[remote]
	server.access.Unlock()
	payload := buildIPv4Packet(session.address, netip.MustParseAddr("1.1.1.1"))
	wrongToken := wrapDataPacket(session, payload)
	wrongToken[2] ^= 0xff
	if server.handleData(remote, wrongToken) {
		t.Fatal("accepted DATA with wrong token")
	}
	wrongSession := wrapDataPacket(session, payload)
	wrongSession[4] ^= 0xff
	if server.handleData(remote, wrongSession) {
		t.Fatal("accepted DATA with wrong session")
	}
}

func TestServerRejectsDataEncryptionModeMismatch(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{{Username: "myuser", Password: "mypassword"}})
	defer cleanup()
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("OPEN failed")
	}
	server.access.Lock()
	session := server.byRemote[remote]
	server.access.Unlock()
	packet := wrapDataPacket(session, buildIPv4Packet(session.address, netip.MustParseAddr("1.1.1.1")))
	packet[0] = packetDataEnc
	if server.handleData(remote, packet) {
		t.Fatal("accepted encrypted DATA for plaintext session")
	}
}

func TestServerAcceptsAuthenticatedEncryptedData(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{{Username: "myuser", Password: "mypassword"}})
	defer cleanup()
	server.options.Encrypt = true
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("OPEN failed")
	}
	server.access.Lock()
	session := server.byRemote[remote]
	server.access.Unlock()
	packet := wrapDataPacket(session, buildIPv4Packet(session.address, netip.MustParseAddr("1.1.1.1")))
	if packet[0] != packetDataEnc {
		t.Fatal("encrypted session produced plaintext DATA")
	}
	if !server.handleData(remote, packet) {
		t.Fatal("rejected authenticated encrypted DATA")
	}
}

func TestServerDataPathAndReturnDemux(t *testing.T) {
	t.Parallel()
	handler := &recordingHandler{
		packetConnections: make(chan packetConnectionRecord, 1),
	}
	server, packetConn, clientConn, remote, cleanup := startManualTestServerWithHandler(t, handler, []ServerUser{
		{Username: "myuser", Password: "mypassword"},
	})
	defer cleanup()
	openPacket, err := buildOpenPacket("myuser", "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, openPacket) {
		t.Fatal("OPEN failed")
	}
	drainPacket(t, clientConn)
	server.access.Lock()
	session := server.byUsername["myuser"]
	server.access.Unlock()
	udpPacket := buildIPv4UDPPacket(session.address, netip.MustParseAddr("198.51.100.1"), 12345, 53, []byte("ping"))
	dataPacket := wrapDataPacket(session, udpPacket)
	if !server.handleData(remote, dataPacket) {
		t.Fatal("DATA was not accepted")
	}
	var record packetConnectionRecord
	select {
	case record = <-handler.packetConnections:
	case <-time.After(time.Second):
		t.Fatal("gVisor did not route DATA to handler")
	}
	if record.source.Addr != session.address || record.destination.Addr != netip.MustParseAddr("198.51.100.1") {
		t.Fatalf("unexpected handler metadata: %+v", record)
	}
	if err = record.conn.WritePacket(buf.As([]byte("pong")), record.destination); err != nil {
		t.Fatal(err)
	}
	response := readPacket(t, clientConn)
	if response[0] != packetData {
		t.Fatalf("unexpected response packet type: %x", response[0])
	}
	payload := append([]byte(nil), response[headerSize:]...)
	source, destination, ok := ipv4SourceDestination(payload)
	if !ok {
		t.Fatal("invalid response IP packet")
	}
	if source != record.destination.Addr || destination != session.address {
		t.Fatalf("unexpected response addresses: %v -> %v", source, destination)
	}
	ipHeaderLen := int(payload[0]&0x0f) * 4
	udpPayload := payload[ipHeaderLen+header.UDPMinimumSize:]
	if string(udpPayload) != "pong" {
		t.Fatalf("unexpected response payload: %q", udpPayload)
	}
}

func TestServerAddressOwnershipPreventsCrossUserReuse(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{
		{Username: "first", Password: "firstpassword"},
		{Username: "second", Password: "secondpassword"},
	})
	defer cleanup()
	firstOpen, err := buildOpenPacket("first", "firstpassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, firstOpen) {
		t.Fatal("first OPEN failed")
	}
	server.access.Lock()
	firstSession := server.byUsername["first"]
	firstAddress := firstSession.address
	closePacket := buildClosePacket(firstSession.token, firstSession.sessionID)
	server.access.Unlock()
	if !server.handleClose(remote, closePacket) {
		t.Fatal("close failed")
	}
	otherClientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer otherClientConn.Close()
	otherRemote := M.SocksaddrFromNet(otherClientConn.LocalAddr()).Unwrap().AddrPort()
	secondOpen, err := buildOpenPacket("second", "secondpassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, otherRemote, secondOpen) {
		t.Fatal("second OPEN failed")
	}
	server.access.Lock()
	defer server.access.Unlock()
	if secondAddress := server.byUsername["second"].address; secondAddress == firstAddress {
		t.Fatalf("reused first user's address for second user: %v", secondAddress)
	}
}

func TestServerStaticAddressesAreReserved(t *testing.T) {
	t.Parallel()
	server, packetConn, remote, cleanup := startManualTestServer(t, []ServerUser{
		{Username: "dynamic1", Password: "password1"},
		{Username: "static", Password: "password2", Address: netip.MustParseAddr("10.66.0.3")},
		{Username: "dynamic2", Password: "password3"},
	})
	defer cleanup()
	dynamicOpen, err := buildOpenPacket("dynamic1", "password1", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, remote, dynamicOpen) {
		t.Fatal("first dynamic OPEN failed")
	}
	otherClientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer otherClientConn.Close()
	otherRemote := M.SocksaddrFromNet(otherClientConn.LocalAddr()).Unwrap().AddrPort()
	secondDynamicOpen, err := buildOpenPacket("dynamic2", "password3", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !server.handleOpen(packetConn, otherRemote, secondDynamicOpen) {
		t.Fatal("second dynamic OPEN failed")
	}
	server.access.Lock()
	defer server.access.Unlock()
	if address := server.byUsername["dynamic2"].address; address == netip.MustParseAddr("10.66.0.3") {
		t.Fatal("dynamic user received another user's static address")
	}
}

func TestServerRejectsDuplicateStaticAddresses(t *testing.T) {
	t.Parallel()
	_, err := NewServer(ServerOptions{
		Context:     t.Context(),
		Logger:      logger.NOP(),
		AddressPool: netip.MustParsePrefix("10.66.0.0/24"),
		Users: []ServerUser{
			{Username: "first", Password: "firstpassword", Address: netip.MustParseAddr("10.66.0.2")},
			{Username: "second", Password: "secondpassword", Address: netip.MustParseAddr("10.66.0.2")},
		},
	})
	if err == nil {
		t.Fatal("accepted duplicate static addresses")
	}
}

func TestServerProtocolHelpers(t *testing.T) {
	t.Parallel()
	openPacket, err := buildOpenPacket("myuser", "mypassword", 1280, true, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseOpenPacket(openPacket)
	if err != nil {
		t.Fatal(err)
	}
	if info.username != "myuser" || info.mtu != 1280 || !info.encrypt || info.pipeID != 7 || info.pipeIndex != 1 {
		t.Fatalf("unexpected OPEN info: %+v", info)
	}
	expectedPassword, err := encryptedPassword("myuser", "mypassword")
	if err != nil {
		t.Fatal(err)
	}
	if info.passwordBlock != expectedPassword {
		t.Fatal("unexpected password block")
	}
	openAck := buildOpenAckPacket([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, 1280, netip.MustParseAddr("10.66.0.2"), true, []netip.Addr{netip.MustParseAddr("1.1.1.1")})
	ackInfo, err := parseOpenAck(openAck)
	if err != nil {
		t.Fatal(err)
	}
	if ackInfo.peerIP != netip.MustParseAddr("10.66.0.2") || ackInfo.serverMTU != 1280 || ackInfo.encrypt != 1 || ackInfo.dns0 != netip.MustParseAddr("1.1.1.1") {
		t.Fatalf("unexpected OPENACK info: %+v", ackInfo)
	}
	reject := buildOpenRejectPacket()
	if len(reject) != signedHeader || reject[0] != packetOpenReject || !verifyPacket(reject) {
		t.Fatalf("invalid OPENREJ packet: %x", reject)
	}
	echo := buildEchoPacket([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, 1, 1, 1, 0, time.Unix(1, 2))
	response := buildEchoResponsePacket(echo)
	if response[0] != packetEchoResp || !verifyPacket(response) || string(response[signedHeader:signedHeader+8]) != string(echo[signedHeader:signedHeader+8]) {
		t.Fatal("invalid ECHORESP packet")
	}
}

func startTestServerAndEndpoint(t *testing.T, encrypt bool) (*Server, *Endpoint, func()) {
	t.Helper()
	ctx := t.Context()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Context:     ctx,
		Logger:      logger.NOP(),
		AddressPool: netip.MustParsePrefix("10.66.0.0/24"),
		Users: []ServerUser{
			{
				Username: "myuser",
				Password: "mypassword",
			},
		},
		MTU:     defaultMTU,
		Encrypt: encrypt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(packetConn); err != nil {
		t.Fatal(err)
	}
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  ctx,
		Logger:   logger.NOP(),
		Dialer:   udpTestDialer{},
		Server:   M.SocksaddrFromNet(packetConn.LocalAddr()).Unwrap(),
		MTU:      defaultMTU,
		Username: "myuser",
		Password: "mypassword",
		Encrypt:  encrypt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = endpoint.Start(); err != nil {
		t.Fatal(err)
	}
	endpoint.onTimer(time.Now().Add(authRetryInterval))
	return server, endpoint, func() {
		_ = endpoint.Close()
		_ = server.Close()
	}
}

func startManualTestServer(t *testing.T, users []ServerUser) (*Server, net.PacketConn, netip.AddrPort, func()) {
	t.Helper()
	server, packetConn, _, remote, cleanup := startManualTestServerWithHandler(t, nil, users)
	return server, packetConn, remote, cleanup
}

func startManualTestServerWithHandler(t *testing.T, handler tun.Handler, users []ServerUser) (*Server, net.PacketConn, net.PacketConn, netip.AddrPort, func()) {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clientConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerOptions{
		Context:     t.Context(),
		Logger:      logger.NOP(),
		Handler:     handler,
		AddressPool: netip.MustParsePrefix("10.66.0.0/24"),
		Users:       users,
		MTU:         defaultMTU,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(packetConn); err != nil {
		t.Fatal(err)
	}
	return server, packetConn, clientConn, M.SocksaddrFromNet(clientConn.LocalAddr()).Unwrap().AddrPort(), func() {
		_ = clientConn.Close()
		_ = server.Close()
	}
}

func buildIPv4Packet(source netip.Addr, destination netip.Addr) []byte {
	packet := make([]byte, 20)
	packet[0] = 0x45
	packet[8] = 64
	packet[9] = 17
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	sourceBytes := source.As4()
	destinationBytes := destination.As4()
	copy(packet[12:16], sourceBytes[:])
	copy(packet[16:20], destinationBytes[:])
	return packet
}

func buildIPv4UDPPacket(source netip.Addr, destination netip.Addr, sourcePort uint16, destinationPort uint16, payload []byte) []byte {
	packet := make([]byte, header.IPv4MinimumSize+header.UDPMinimumSize+len(payload))
	ipHeader := header.IPv4(packet[:header.IPv4MinimumSize])
	ipHeader.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(packet)),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     tun.AddressFromAddr(source),
		DstAddr:     tun.AddressFromAddr(destination),
	})
	ipHeader.SetChecksum(^ipHeader.CalculateChecksum())
	udpHeader := header.UDP(packet[header.IPv4MinimumSize : header.IPv4MinimumSize+header.UDPMinimumSize])
	udpHeader.Encode(&header.UDPFields{
		SrcPort: sourcePort,
		DstPort: destinationPort,
		Length:  uint16(header.UDPMinimumSize + len(payload)),
	})
	copy(packet[header.IPv4MinimumSize+header.UDPMinimumSize:], payload)
	return packet
}

func drainPacket(t *testing.T, conn net.PacketConn) {
	t.Helper()
	_ = readPacket(t, conn)
}

func readPacket(t *testing.T, conn net.PacketConn) []byte {
	t.Helper()
	buffer := make([]byte, fragmentOutputSize)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := conn.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, n)
	copy(packet, buffer[:n])
	return packet
}

type packetConnectionRecord struct {
	conn        N.PacketConn
	source      M.Socksaddr
	destination M.Socksaddr
}

type recordingHandler struct {
	packetConnections chan packetConnectionRecord
}

func (h *recordingHandler) JudgeFlow(network uint8, source netip.AddrPort, destination netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
	return tun.FlowVerdict{Action: tun.ActionAccept}
}

func (h *recordingHandler) NewDNSPacket(payload []byte, source M.Socksaddr, destination M.Socksaddr, writer N.PacketWriter) {
}

func (h *recordingHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
}

func (h *recordingHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.packetConnections <- packetConnectionRecord{
		conn:        conn,
		source:      source,
		destination: destination,
	}
}

type udpTestDialer struct{}

func (d udpTestDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return net.DialUDP("udp", nil, destination.UDPAddr())
}

func (d udpTestDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (d udpTestDialer) Upstream() any {
	return nil
}

func (d udpTestDialer) Start() error {
	return nil
}

func (d udpTestDialer) Close() error {
	return nil
}

func (d udpTestDialer) InterfaceUpdated() {
}

func (d udpTestDialer) Addr() netip.Addr {
	return netip.Addr{}
}
