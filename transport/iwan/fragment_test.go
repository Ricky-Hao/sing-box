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

func TestFragmentReassemblyThreeFragmentsHonorsOffsets(t *testing.T) {
	t.Parallel()
	var reassembler fragmentReassembler
	fragments := [][]byte{
		buildFragmentPacket(0x12345678, false, 0, []byte("abcd")),
		buildFragmentPacket(0x12345678, false, 4, []byte("efgh")),
		buildFragmentPacket(0x12345678, true, 8, []byte("ijkl")),
	}
	for index, fragment := range fragments {
		payload, ok := reassembler.handle(fragment)
		if !ok {
			t.Fatalf("fragment %d was rejected", index)
		}
		if index < len(fragments)-1 && payload != nil {
			t.Fatalf("fragment %d emitted incomplete payload %q", index, payload)
		}
		if index == len(fragments)-1 && string(payload) != "abcdefghijkl" {
			t.Fatalf("three-fragment reassembly corrupted payload: got %q, want %q", payload, "abcdefghijkl")
		}
	}
}

func TestFragmentReassemblyRejectsInvalidCoverageAndResetsSlot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		offset int
	}{
		{name: "overlap", offset: 2},
		{name: "gap", offset: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reassembler fragmentReassembler
			first := buildFragmentPacket(0x12345678, false, 0, []byte("abcd"))
			if payload, ok := reassembler.handle(first); !ok || payload != nil {
				t.Fatalf("unexpected first fragment result: payload=%v ok=%v", payload, ok)
			}
			invalid := buildFragmentPacket(0x12345678, true, test.offset, []byte("efgh"))
			if payload, ok := reassembler.handle(invalid); ok || payload != nil {
				t.Fatalf("invalid %s emitted payload: payload=%q ok=%v", test.name, payload, ok)
			}
			if payload, ok := reassembler.handle(first); !ok || payload != nil {
				t.Fatalf("slot was not reset after %s: payload=%v ok=%v", test.name, payload, ok)
			}
			last := buildFragmentPacket(0x12345678, true, 4, []byte("efgh"))
			payload, ok := reassembler.handle(last)
			if !ok || string(payload) != "abcdefgh" {
				t.Fatalf("valid packet after %s failed: payload=%q ok=%v", test.name, payload, ok)
			}
		})
	}
}

func TestFragmentReassemblyRejectsOutOfOrderAndMalformedFragments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fragment []byte
	}{
		{name: "initial nonzero offset", fragment: buildFragmentPacket(1, false, 4, []byte("efgh"))},
		{name: "initial terminal fragment", fragment: buildFragmentPacket(1, true, 0, []byte("abcd"))},
		{name: "zero length", fragment: buildFragmentPacket(1, false, 0, nil)},
		{name: "declared length mismatch", fragment: buildFragmentPacket(1, false, 0, []byte("abcd"))[:headerSize+ethPacketSize+3]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reassembler fragmentReassembler
			if payload, ok := reassembler.handle(test.fragment); ok || payload != nil {
				t.Fatalf("malformed fragment emitted payload: payload=%q ok=%v", payload, ok)
			}
		})
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
