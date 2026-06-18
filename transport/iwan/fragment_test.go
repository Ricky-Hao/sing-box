//go:build with_gvisor

package iwan

import (
	"encoding/binary"
	"testing"
)

func TestFragmentReassembly(t *testing.T) {
	t.Parallel()
	var reassembler fragmentReassembler
	first := buildFragmentPacket(0x12345678, false, 0, []byte("hello "))
	payload, ok := reassembler.handle(first)
	if !ok || payload != nil {
		t.Fatalf("unexpected first fragment result: payload=%v ok=%v", payload, ok)
	}
	second := buildFragmentPacket(0x12345678, true, 6, []byte("world"))
	payload, ok = reassembler.handle(second)
	if !ok {
		t.Fatal("second fragment was rejected")
	}
	if string(payload) != "hello world" {
		t.Fatalf("unexpected payload: %q", payload)
	}
}

func buildFragmentPacket(id uint32, endOfPacket bool, offset int, payload []byte) []byte {
	packet := make([]byte, headerSize+ethPacketSize+len(payload))
	packet[0] = packetIPFrag
	binary.LittleEndian.PutUint32(packet[headerSize+8:headerSize+12], id)
	bitfield := uint32(offset&0x1fff)<<2 | uint32(len(payload)&0x7ff)<<15
	if endOfPacket {
		bitfield |= 1
	}
	binary.LittleEndian.PutUint32(packet[headerSize+12:headerSize+16], bitfield)
	copy(packet[headerSize+ethPacketSize:], payload)
	return packet
}
