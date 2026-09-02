//go:build with_gvisor

package iwan

import (
	"encoding/binary"
	"net"
	"time"
)

type fragmentReassembler struct {
	slots [fragmentSlots]fragmentSlot
}

type fragmentSlot struct {
	buffer    [fragmentBufferSize]byte
	timestamp time.Time
	id        uint32
	length    uint16
	inUse     bool
}

func (e *Endpoint) handleIPFrag(conn net.Conn, packet []byte) bool {
	e.access.Lock()
	if e.conn != conn || !e.ready.Load() || !e.matchesSessionLocked(packet) {
		e.access.Unlock()
		return false
	}
	xorKey := e.xorKey
	encrypt := e.encrypt
	e.fragmentLock.Lock()
	payload, ok := e.fragments.handle(packet)
	e.fragmentLock.Unlock()
	e.access.Unlock()
	if !ok {
		return false
	}
	if payload == nil {
		return true
	}
	if encrypt {
		xorData(xorKey, payload)
	}
	if e.returnPacket(payload) {
		return true
	}
	if err := e.device.Write(payload); err != nil {
		e.options.Logger.Error(err)
		return false
	}
	return true
}

func (r *fragmentReassembler) handle(packet []byte) ([]byte, bool) {
	if len(packet) < headerSize+ethPacketSize {
		return nil, false
	}
	ethPacket := packet[headerSize : headerSize+ethPacketSize]
	fragmentID := binary.LittleEndian.Uint32(ethPacket[8:12])
	bitfield := binary.LittleEndian.Uint32(ethPacket[12:16])
	endOfPacket := bitfield&1 != 0
	fragmentOffset := int((bitfield >> 2) & 0x1fff)
	fragmentLength := int((bitfield >> 15) & 0x7ff)
	if fragmentLength == 0 || len(packet) != headerSize+ethPacketSize+fragmentLength {
		return nil, false
	}
	fragmentData := packet[headerSize+ethPacketSize:]
	now := time.Now()
	for index := range r.slots {
		slot := &r.slots[index]
		if !slot.inUse || slot.id != fragmentID {
			continue
		}
		if now.Sub(slot.timestamp) >= fragmentTimeout {
			slot.inUse = false
			break
		}
		if fragmentOffset != int(slot.length) {
			slot.inUse = false
			return nil, false
		}
		totalLength := fragmentOffset + fragmentLength
		if endOfPacket {
			if totalLength > fragmentOutputSize {
				slot.inUse = false
				return nil, false
			}
			output := make([]byte, totalLength)
			copy(output, slot.buffer[:slot.length])
			copy(output[fragmentOffset:], fragmentData)
			slot.inUse = false
			return output, true
		}
		if totalLength <= fragmentBufferSize {
			copy(slot.buffer[fragmentOffset:], fragmentData)
			slot.length = uint16(totalLength)
			slot.timestamp = now
			return nil, true
		}
		slot.inUse = false
		return nil, false
	}
	slot := r.emptySlot(now)
	if slot == nil {
		return nil, false
	}
	if endOfPacket || fragmentOffset != 0 || fragmentLength > fragmentBufferSize {
		return nil, false
	}
	slot.inUse = true
	slot.id = fragmentID
	slot.timestamp = now
	slot.length = uint16(fragmentLength)
	copy(slot.buffer[:], fragmentData)
	return nil, true
}

func (r *fragmentReassembler) emptySlot(now time.Time) *fragmentSlot {
	for index := range r.slots {
		slot := &r.slots[index]
		if !slot.inUse || now.Sub(slot.timestamp) >= fragmentTimeout {
			return slot
		}
	}
	return nil
}
