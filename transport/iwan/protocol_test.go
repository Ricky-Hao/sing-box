package iwan

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func TestBuildOpenPacket(t *testing.T) {
	t.Parallel()
	packet, err := buildOpenPacket("myuser", "mypassword", 1400, true, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if packet[0] != packetOpen {
		t.Fatal("unexpected packet type")
	}
	if packet[1] != 1 {
		t.Fatal("unexpected encrypt flag")
	}
	if !verifyPacket(packet) {
		t.Fatal("OPEN signature mismatch")
	}
	tlvs := collectTLVs(t, packet[signedHeader:])
	if !bytes.Equal(tlvs[3], []byte{0x05, 0x78}) {
		t.Fatalf("unexpected MTU TLV: %x", tlvs[3])
	}
	if string(tlvs[1]) != "myuser" {
		t.Fatalf("unexpected username TLV: %q", tlvs[1])
	}
	if len(tlvs[2]) != 16 {
		t.Fatalf("unexpected password TLV length: %d", len(tlvs[2]))
	}
	if !bytes.Equal(tlvs[8], []byte{1}) {
		t.Fatalf("unexpected encrypt TLV: %x", tlvs[8])
	}
	if !bytes.Equal(tlvs[0x0a], []byte{0x80, 0x07}) {
		t.Fatalf("unexpected pipe TLV: %x", tlvs[0x0a])
	}
}

func TestXORKeyRoundTrip(t *testing.T) {
	t.Parallel()
	key := deriveXORKey("myuser", "mypassword")
	payload := []byte("hello iwan")
	encrypted := append([]byte(nil), payload...)
	xorData(key, encrypted)
	if bytes.Equal(encrypted, payload) {
		t.Fatal("XOR did not change payload")
	}
	xorData(key, encrypted)
	if !bytes.Equal(encrypted, payload) {
		t.Fatal("XOR round trip failed")
	}
}

func TestParseOpenAck(t *testing.T) {
	t.Parallel()
	packet := make([]byte, signedHeader)
	packet[0] = packetOpenAck
	copy(packet[2:4], []byte{0x12, 0x34})
	copy(packet[4:8], []byte{0xde, 0xad, 0xbe, 0xef})
	signPacket(packet)
	packet = appendTLV(packet, 3, 0x05, 0x78)
	packet = appendTLV(packet, 4, 10, 20, 30, 40)
	packet = appendTLV(packet, 5, 1, 1, 1, 1)
	packet = appendTLV(packet, 6, 8, 8, 8, 8, 9, 9, 9, 9)
	packet = appendTLV(packet, 8, 1)
	info, err := parseOpenAck(packet)
	if err != nil {
		t.Fatal(err)
	}
	if info.serverMTU != 1400 {
		t.Fatalf("unexpected mtu: %d", info.serverMTU)
	}
	if info.peerIP != netip.MustParseAddr("10.20.30.40") {
		t.Fatalf("unexpected peer ip: %v", info.peerIP)
	}
	if info.dns0 != netip.MustParseAddr("8.8.8.8") || info.dns1 != netip.MustParseAddr("9.9.9.9") {
		t.Fatalf("unexpected dns: %v %v", info.dns0, info.dns1)
	}
	if info.encrypt != 1 {
		t.Fatalf("unexpected encrypt flag: %d", info.encrypt)
	}
}

func TestBuildEchoPacket(t *testing.T) {
	t.Parallel()
	packet := buildEchoPacket([2]byte{1, 2}, [4]byte{3, 4, 5, 6}, 7, 8, 9, 0x10203040, time.Unix(10, 0))
	if len(packet) != signedHeader+36 {
		t.Fatalf("unexpected echo length: %d", len(packet))
	}
	if packet[0] != packetEchoReq || !verifyPacket(packet) {
		t.Fatal("invalid echo packet")
	}
	payload := packet[signedHeader:]
	if binary.LittleEndian.Uint64(payload[:8]) != uint64(time.Unix(10, 0).UnixMicro()) {
		t.Fatal("unexpected timestamp")
	}
	if !bytes.Equal(payload[24:28], []byte{'T', 'D', 'R', 0}) {
		t.Fatalf("unexpected route tag: %x", payload[24:28])
	}
	if binary.BigEndian.Uint32(payload[28:32]) != 0x10203040 {
		t.Fatal("unexpected route magic")
	}
}

func TestParseControlPacketsRejectMalformedInput(t *testing.T) {
	validOpen, err := buildOpenPacket("myuser", "mypassword", 1400, true, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	validAck := buildOpenAckPacket([2]byte{1, 2}, [4]byte{3, 4, 5, 6}, 1400, netip.MustParseAddr("10.66.0.2"), true, nil)
	tests := []struct {
		name  string
		parse func([]byte) error
		data  []byte
	}{
		{"short OPEN", func(packet []byte) error { _, parseErr := parseOpenPacket(packet); return parseErr }, []byte{packetOpen}},
		{"bad OPEN signature", func(packet []byte) error { _, parseErr := parseOpenPacket(packet); return parseErr }, append([]byte(nil), validOpen...)},
		{"truncated OPEN TLV", func(packet []byte) error { _, parseErr := parseOpenPacket(packet); return parseErr }, append(append([]byte(nil), validOpen...), 1)},
		{"zero OPEN TLV length", func(packet []byte) error { _, parseErr := parseOpenPacket(packet); return parseErr }, append(append([]byte(nil), validOpen...), 1, 0)},
		{"short OPENACK", func(packet []byte) error { _, parseErr := parseOpenAck(packet); return parseErr }, []byte{packetOpenAck}},
		{"bad OPENACK signature", func(packet []byte) error { _, parseErr := parseOpenAck(packet); return parseErr }, append([]byte(nil), validAck...)},
		{"truncated OPENACK TLV", func(packet []byte) error { _, parseErr := parseOpenAck(packet); return parseErr }, append(append([]byte(nil), validAck...), 4)},
		{"oversized OPENACK TLV", func(packet []byte) error { _, parseErr := parseOpenAck(packet); return parseErr }, append(append([]byte(nil), validAck...), 4, 9)},
	}
	validOpenCopy := tests[1].data
	validOpenCopy[headerSize] ^= 0xff
	validAckCopy := tests[5].data
	validAckCopy[headerSize] ^= 0xff
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if parseErr := test.parse(test.data); parseErr == nil {
				t.Fatal("accepted malformed control packet")
			}
		})
	}
}

func collectTLVs(t *testing.T, data []byte) map[byte][]byte {
	t.Helper()
	tlvs := make(map[byte][]byte)
	for len(data) > 0 {
		if len(data) < 2 {
			t.Fatal("truncated TLV")
		}
		tlvType := data[0]
		tlvLength := int(data[1])
		if tlvLength < 2 || tlvLength > len(data) {
			t.Fatalf("invalid TLV length %d", tlvLength)
		}
		tlvs[tlvType] = append([]byte(nil), data[2:tlvLength]...)
		data = data[tlvLength:]
	}
	return tlvs
}
