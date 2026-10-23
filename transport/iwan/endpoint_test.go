//go:build with_gvisor

package iwan

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv4"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

func TestEndpointHandshakeAndCloseReconnect(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dialer := &pipeDialer{
		servers: make(chan net.Conn, 2),
	}
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  ctx,
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", defaultPort),
		MTU:      defaultMTU,
		Username: "myuser",
		Password: "mypassword",
		Encrypt:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if err = endpoint.Start(); err != nil {
		t.Fatal(err)
	}
	server := <-dialer.servers
	triggerOpen(t, endpoint, server)
	openAck := buildTestOpenAck([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, netip.MustParseAddr("10.20.30.40"))
	if _, err = server.Write(openAck); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return endpoint.Ready()
	})
	closePacket := buildClosePacket([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef})
	if _, err = server.Write(closePacket); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return !endpoint.Ready()
	})
	endpoint.onTimer(time.Now().Add(time.Second))
	reconnectedServer := <-dialer.servers
	triggerOpen(t, endpoint, reconnectedServer)
}

func TestEndpointIgnoresOpenRejectAfterEstablished(t *testing.T) {
	t.Parallel()
	endpoint, server, cleanup := startTestEndpoint(t)
	defer cleanup()
	establishEndpoint(t, endpoint, server, [2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, netip.MustParseAddr("10.20.30.40"))
	endpoint.access.Lock()
	conn := endpoint.conn
	endpoint.access.Unlock()
	openReject := make([]byte, signedHeader)
	openReject[0] = packetOpenReject
	signPacket(openReject)
	if endpoint.handlePacket(conn, openReject) {
		t.Fatal("accepted OPENREJ outside auth state")
	}
	if !endpoint.Ready() {
		t.Fatal("endpoint left ready state after stale OPENREJ")
	}
	endpoint.access.Lock()
	state := endpoint.state
	endpoint.access.Unlock()
	if state != stateEstablished {
		t.Fatalf("unexpected state after stale OPENREJ: %v", state)
	}
}

func TestEndpointIgnoresStaleOpenAck(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dialer := &pipeDialer{
		servers: make(chan net.Conn, 3),
	}
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  ctx,
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", defaultPort),
		MTU:      defaultMTU,
		Username: "myuser",
		Password: "mypassword",
		Encrypt:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if err = endpoint.Start(); err != nil {
		t.Fatal(err)
	}
	server := <-dialer.servers
	triggerOpen(t, endpoint, server)
	endpoint.access.Lock()
	oldConn := endpoint.conn
	endpoint.access.Unlock()
	if err = endpoint.reconnect(time.Now()); err != nil {
		t.Fatal(err)
	}
	reconnectedServer := <-dialer.servers
	openAck := buildTestOpenAck([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, netip.MustParseAddr("10.20.30.40"))
	if endpoint.handlePacket(oldConn, openAck) {
		t.Fatal("accepted stale OPENACK from old connection")
	}
	if endpoint.Ready() {
		t.Fatal("endpoint became ready from stale OPENACK")
	}
	endpoint.access.Lock()
	state := endpoint.state
	localAddress := endpoint.localAddress
	endpoint.access.Unlock()
	if state != stateAuthSent {
		t.Fatalf("unexpected state after stale OPENACK: %v", state)
	}
	if localAddress.IsValid() {
		t.Fatalf("unexpected local address from stale OPENACK: %v", localAddress)
	}
	triggerOpen(t, endpoint, reconnectedServer)
}

func TestEndpointRejectsWrongSessionAndEncryptionMode(t *testing.T) {
	t.Parallel()
	endpoint, server, cleanup := startTestEndpoint(t)
	defer cleanup()
	token := [2]byte{0x12, 0x34}
	sessionID := [4]byte{0xde, 0xad, 0xbe, 0xef}
	establishEndpoint(t, endpoint, server, token, sessionID, netip.MustParseAddr("10.20.30.40"))
	endpoint.access.Lock()
	conn := endpoint.conn
	xorKey := endpoint.xorKey
	endpoint.access.Unlock()
	payload := buildIPv4Packet(netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.20.30.40"))
	wrongToken := buildDataPacket(token, sessionID, xorKey, true, payload, nil)
	wrongToken[2] ^= 0xff
	if endpoint.handlePacket(conn, wrongToken) {
		t.Fatal("accepted DATA with wrong token")
	}
	wrongSession := buildDataPacket(token, sessionID, xorKey, true, payload, nil)
	wrongSession[4] ^= 0xff
	if endpoint.handlePacket(conn, wrongSession) {
		t.Fatal("accepted DATA with wrong session")
	}
	plaintext := buildDataPacket(token, sessionID, xorKey, false, payload, nil)
	if endpoint.handlePacket(conn, plaintext) {
		t.Fatal("accepted plaintext DATA for encrypted session")
	}
}

func TestEndpointConnectedUDPRejectsOpenAckFromUnexpectedSource(t *testing.T) {
	t.Parallel()
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  t.Context(),
		Logger:   logger.NOP(),
		Dialer:   udpTestDialer{},
		Server:   M.SocksaddrFromNet(server.LocalAddr()).Unwrap(),
		Username: "myuser",
		Password: "mypassword",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if err = endpoint.Start(); err != nil {
		t.Fatal(err)
	}
	endpoint.onTimer(time.Now().Add(authRetryInterval))
	buffer := make([]byte, fragmentOutputSize)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	_, clientAddress, err := server.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	openAck := buildTestOpenAck([2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, netip.MustParseAddr("10.20.30.40"))
	if _, err = attacker.WriteTo(openAck, clientAddress); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if endpoint.Ready() {
		t.Fatal("accepted OPENACK from unexpected UDP source")
	}
	if _, err = server.WriteTo(openAck, clientAddress); err != nil {
		t.Fatal(err)
	}
	waitFor(t, endpoint.Ready)
}

func TestEndpointDataTimeoutSendsClose(t *testing.T) {
	t.Parallel()
	endpoint, server, cleanup := startTestEndpoint(t)
	defer cleanup()
	token := [2]byte{0x12, 0x34}
	sessionID := [4]byte{0xde, 0xad, 0xbe, 0xef}
	establishEndpoint(t, endpoint, server, token, sessionID, netip.MustParseAddr("10.20.30.40"))
	endpoint.access.Lock()
	endpoint.lastRecv = time.Now().Add(-dataTimeout - time.Second)
	endpoint.access.Unlock()
	done := make(chan struct{})
	go func() {
		endpoint.onTimer(time.Now())
		close(done)
	}()
	buffer := make([]byte, signedHeader)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, err := server.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if n != signedHeader || buffer[0] != packetClose || !verifyPacket(buffer[:n]) {
		t.Fatalf("unexpected CLOSE packet: n=%d type=%x", n, buffer[0])
	}
	if buffer[2] != token[0] || buffer[3] != token[1] ||
		buffer[4] != sessionID[0] || buffer[5] != sessionID[1] ||
		buffer[6] != sessionID[2] || buffer[7] != sessionID[3] {
		t.Fatal("CLOSE packet used wrong session")
	}
	select {
	case <-dialerFromEndpoint(endpoint).servers:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not reconnect after data timeout")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout reconnect did not return")
	}
}

func TestEndpointWaitReadyWaitsForOpenAck(t *testing.T) {
	t.Parallel()
	endpoint, server, cleanup := startTestEndpoint(t)
	defer cleanup()

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() {
		ready <- endpoint.WaitReady(waitCtx)
	}()

	select {
	case err := <-ready:
		t.Fatalf("WaitReady returned before OPENACK: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	establishEndpoint(t, endpoint, server, [2]byte{0x12, 0x34}, [4]byte{0xde, 0xad, 0xbe, 0xef}, netip.MustParseAddr("10.20.30.40"))
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitReady did not return after OPENACK")
	}
}

func TestEndpointWaitReadyReturnsContextError(t *testing.T) {
	t.Parallel()
	endpoint, _, cleanup := startTestEndpoint(t)
	defer cleanup()

	waitCtx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	if err := endpoint.WaitReady(waitCtx); err == nil {
		t.Fatal("expected context error")
	}
}

func TestEndpointFlowPortWritesAndReturnsPackets(t *testing.T) {
	t.Parallel()
	endpoint, server, cleanup := startTestEndpoint(t)
	defer cleanup()
	token := [2]byte{0x12, 0x34}
	sessionID := [4]byte{0xde, 0xad, 0xbe, 0xef}
	address := netip.MustParseAddr("10.20.30.40")
	establishEndpoint(t, endpoint, server, token, sessionID, address)
	inet4, inet6 := endpoint.PortAddresses()
	if inet4 != address || inet6.IsValid() {
		t.Fatalf("unexpected port addresses: %v %v", inet4, inet6)
	}
	if endpoint.PortMTU() != defaultMTU {
		t.Fatalf("unexpected port MTU: %d", endpoint.PortMTU())
	}
	outboundPayload := buildIPv4Packet(address, netip.MustParseAddr("1.1.1.1"))
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- endpoint.WritePackets([][]byte{outboundPayload})
	}()
	buffer := make([]byte, fragmentOutputSize)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, err := server.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if err = <-writeDone; err != nil {
		t.Fatal(err)
	}
	if buffer[0] != packetDataEnc {
		t.Fatalf("unexpected outbound packet type: %x", buffer[0])
	}
	decoded := append([]byte(nil), buffer[headerSize:n]...)
	xorData(endpoint.xorKey, decoded)
	if string(decoded) != string(outboundPayload) {
		t.Fatal("flow port changed outbound payload")
	}
	returnPath := &recordingReturnPath{packets: make(chan []byte, 1)}
	if err = endpoint.AttachReturn(returnPath); err != nil {
		t.Fatal(err)
	}
	inboundPayload := buildIPv4Packet(netip.MustParseAddr("1.1.1.1"), address)
	inboundPacket := buildDataPacket(token, sessionID, endpoint.xorKey, true, inboundPayload, nil)
	if _, err = server.Write(inboundPacket); err != nil {
		t.Fatal(err)
	}
	select {
	case returned := <-returnPath.packets:
		if string(returned) != string(inboundPayload) {
			t.Fatal("return path changed inbound payload")
		}
	case <-time.After(time.Second):
		t.Fatal("inbound packet was not offered to return path")
	}
	if err = endpoint.DetachReturn(returnPath); err != nil {
		t.Fatal(err)
	}
}

func TestStackDeviceReplacesLocalAddress(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  ctx,
		Logger:   logger.NOP(),
		Dialer:   &pipeDialer{servers: make(chan net.Conn, 1)},
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", defaultPort),
		MTU:      defaultMTU,
		Username: "myuser",
		Password: "mypassword",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	first := netip.MustParsePrefix("10.20.30.40/24")
	second := netip.MustParsePrefix("10.20.31.40/24")
	if err = endpoint.device.SetLocalAddress(first); err != nil {
		t.Fatal(err)
	}
	if !stackHasAddress(endpoint.device, first.Addr()) {
		t.Fatal("first local address was not added")
	}
	if err = endpoint.device.SetLocalAddress(second); err != nil {
		t.Fatal(err)
	}
	if stackHasAddress(endpoint.device, first.Addr()) {
		t.Fatal("first local address was not removed")
	}
	if !stackHasAddress(endpoint.device, second.Addr()) {
		t.Fatal("second local address was not added")
	}
}

func stackHasAddress(device *stackDevice, address netip.Addr) bool {
	tcpipAddress := tun.AddressFromAddr(address)
	for _, addresses := range device.stack.AllAddresses() {
		for _, protocolAddress := range addresses {
			if protocolAddress.Protocol == ipv4.ProtocolNumber && protocolAddress.AddressWithPrefix.Address == tcpipAddress {
				return true
			}
		}
	}
	return false
}

func startTestEndpoint(t *testing.T) (*Endpoint, net.Conn, func()) {
	t.Helper()
	ctx := t.Context()
	dialer := &pipeDialer{
		servers: make(chan net.Conn, 2),
	}
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:  ctx,
		Logger:   logger.NOP(),
		Dialer:   dialer,
		Server:   M.ParseSocksaddrHostPort("127.0.0.1", defaultPort),
		MTU:      defaultMTU,
		Username: "myuser",
		Password: "mypassword",
		Encrypt:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = endpoint.Start(); err != nil {
		t.Fatal(err)
	}
	server := <-dialer.servers
	return endpoint, server, func() {
		_ = server.Close()
		_ = endpoint.Close()
	}
}

func dialerFromEndpoint(endpoint *Endpoint) *pipeDialer {
	return endpoint.options.Dialer.(*pipeDialer)
}

func establishEndpoint(t *testing.T, endpoint *Endpoint, server net.Conn, token [2]byte, sessionID [4]byte, address netip.Addr) {
	t.Helper()
	triggerOpen(t, endpoint, server)
	openAck := buildTestOpenAck(token, sessionID, address)
	if _, err := server.Write(openAck); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return endpoint.Ready()
	})
}

func buildTestOpenAck(token [2]byte, sessionID [4]byte, address netip.Addr) []byte {
	openAck := make([]byte, signedHeader)
	openAck[0] = packetOpenAck
	copy(openAck[2:4], token[:])
	copy(openAck[4:8], sessionID[:])
	signPacket(openAck)
	openAck = appendTLV(openAck, 3, 0x05, 0x78)
	addressBytes := address.As4()
	openAck = appendTLV(openAck, 4, addressBytes[:]...)
	openAck = appendTLV(openAck, 8, 1)
	return openAck
}

func triggerOpen(t *testing.T, endpoint *Endpoint, server net.Conn) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		endpoint.onTimer(time.Now().Add(authRetryInterval))
		close(done)
	}()
	buffer := make([]byte, fragmentOutputSize)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, err := server.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if n < signedHeader || buffer[0] != packetOpen || !verifyPacket(buffer[:n]) {
		t.Fatalf("unexpected OPEN packet: n=%d type=%x", n, buffer[0])
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OPEN sender did not return")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}

type pipeDialer struct {
	servers chan net.Conn
}

type recordingReturnPath struct {
	packets chan []byte
}

func (r *recordingReturnPath) ReturnHeadroom() int {
	return 4
}

func (r *recordingReturnPath) ReturnPackets(packets [][]byte) [][]byte {
	for _, packet := range packets {
		r.packets <- append([]byte(nil), packet[r.ReturnHeadroom():]...)
	}
	return packets[:0]
}

func (d *pipeDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	d.servers <- server
	return client, nil
}

func (d *pipeDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (d *pipeDialer) Upstream() any {
	return nil
}

func (d *pipeDialer) Start() error {
	return nil
}

func (d *pipeDialer) Close() error {
	return nil
}

func (d *pipeDialer) InterfaceUpdated() {
}

func (d *pipeDialer) Addr() netip.Addr {
	return netip.Addr{}
}
