package iwan

import (
	"bytes"
	"crypto/aes"
	"crypto/md5"
	"encoding/binary"
	"net/netip"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	packetOpenReject = 0x11
	packetOpenAck    = 0x12
	packetOpen       = 0x13
	packetData       = 0x14
	packetEchoReq    = 0x15
	packetEchoResp   = 0x16
	packetClose      = 0x17
	packetDataEnc    = 0x18
	packetIPFrag     = 0x21
	packetIPFragSR   = 0x22
	packetSEGRT      = 0x27

	headerSize    = 8
	signSize      = 16
	signedHeader  = headerSize + signSize
	ethPacketSize = 16

	defaultPort = 4567
	defaultMTU  = 1400
	minMTU      = 46
	maxMTU      = 1600

	authTimeout        = 6 * time.Second
	authRetryInterval  = 2 * time.Second
	dataTimeout        = 15 * time.Second
	fragmentSlots      = 10
	fragmentBufferSize = 2048
	fragmentOutputSize = 4096
	fragmentTimeout    = 5 * time.Second
)

type endpointState uint8

const (
	stateNotReady    endpointState = 0
	stateDNSNeeded   endpointState = 2
	stateIPReady     endpointState = 3
	stateAuthSent    endpointState = 4
	stateEstablished endpointState = 5
	stateClosed      endpointState = 6
)

type openAckInfo struct {
	serverMTU  uint16
	peerIP     netip.Addr
	dns0       netip.Addr
	dns1       netip.Addr
	peerDupPkt uint8
	encrypt    uint8
}

type openInfo struct {
	mtu           uint16
	username      string
	passwordBlock [16]byte
	encrypt       bool
	pipeID        uint16
	pipeIndex     uint8
}

func signPacket(packet []byte) {
	digest := md5.New()
	digest.Write(packet[:headerSize])
	digest.Write([]byte("mw"))
	copy(packet[headerSize:signedHeader], digest.Sum(nil))
}

func verifyPacket(packet []byte) bool {
	if len(packet) < signedHeader {
		return false
	}
	digest := md5.New()
	digest.Write(packet[:headerSize])
	digest.Write([]byte("mw"))
	return bytes.Equal(packet[headerSize:signedHeader], digest.Sum(nil))
}

func deriveXORKey(username string, password string) [8]byte {
	hash := md5.Sum([]byte(username + password))
	var key [8]byte
	copy(key[:], hash[:8])
	return key
}

func xorData(key [8]byte, data []byte) {
	for index := range data {
		data[index] ^= key[index&7]
	}
}

func encryptedPassword(username string, password string) ([16]byte, error) {
	key := md5.Sum(append([]byte("mw"), []byte(username)...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return [16]byte{}, err
	}
	var plain [16]byte
	copy(plain[:], []byte(password))
	var encrypted [16]byte
	block.Encrypt(encrypted[:], plain[:])
	return encrypted, nil
}

func buildOpenPacket(username string, password string, mtu uint32, encrypt bool, pipeID uint16, pipeIndex uint8) ([]byte, error) {
	if mtu == 0 {
		mtu = defaultMTU
	}
	if mtu < minMTU || mtu > maxMTU {
		return nil, E.New("invalid MTU: ", mtu, ", required ", minMTU, "-", maxMTU)
	}
	if pipeIndex > 1 {
		return nil, E.New("invalid pipe_index: ", pipeIndex, ", required 0 or 1")
	}
	packet := make([]byte, signedHeader, 128)
	packet[0] = packetOpen
	if encrypt {
		packet[1] = 1
	}
	signPacket(packet)
	packet = appendTLV(packet, 3, byte(mtu>>8), byte(mtu))
	packet = appendTLV(packet, 1, []byte(username)...)
	passwordBlock, err := encryptedPassword(username, password)
	if err != nil {
		return nil, err
	}
	packet = appendTLV(packet, 2, passwordBlock[:]...)
	if encrypt {
		packet = appendTLV(packet, 8, 1)
	}
	if pipeID != 0 || pipeIndex != 0 {
		value := pipeID | uint16(pipeIndex)<<15
		packet = appendTLV(packet, 0x0a, byte(value>>8), byte(value))
	}
	return packet, nil
}

func appendTLV(packet []byte, tlvType byte, value ...byte) []byte {
	packet = append(packet, tlvType, byte(len(value)+2))
	return append(packet, value...)
}

func parseOpenPacket(packet []byte) (openInfo, error) {
	if len(packet) < signedHeader {
		return openInfo{}, E.New("short OPEN")
	}
	if !verifyPacket(packet) {
		return openInfo{}, E.New("invalid OPEN signature")
	}
	info := openInfo{
		mtu:     defaultMTU,
		encrypt: packet[1] != 0,
	}
	tlvs := packet[signedHeader:]
	for len(tlvs) > 0 {
		if len(tlvs) < 2 {
			break
		}
		tlvType := tlvs[0]
		tlvLength := int(tlvs[1])
		if tlvLength < 2 || tlvLength > len(tlvs) {
			break
		}
		value := tlvs[2:tlvLength]
		switch tlvType {
		case 1:
			info.username = string(value)
		case 2:
			if len(value) >= len(info.passwordBlock) {
				copy(info.passwordBlock[:], value[:len(info.passwordBlock)])
			}
		case 3:
			if len(value) >= 2 {
				info.mtu = binary.BigEndian.Uint16(value[:2])
			}
		case 8:
			info.encrypt = len(value) > 0 && value[0] != 0
		case 0x0a:
			if len(value) >= 2 {
				pipe := binary.BigEndian.Uint16(value[:2])
				info.pipeID = pipe & 0x7fff
				info.pipeIndex = uint8(pipe >> 15)
			}
		}
		tlvs = tlvs[tlvLength:]
	}
	if info.username == "" {
		return openInfo{}, E.New("OPEN missing username")
	}
	if info.passwordBlock == ([16]byte{}) {
		return openInfo{}, E.New("OPEN missing password")
	}
	if info.mtu < minMTU || info.mtu > maxMTU {
		return openInfo{}, E.New("invalid OPEN MTU: ", info.mtu)
	}
	return info, nil
}

func buildOpenAckPacket(token [2]byte, sessionID [4]byte, mtu uint32, address netip.Addr, encrypt bool, dns []netip.Addr) []byte {
	packet := make([]byte, signedHeader, 64)
	packet[0] = packetOpenAck
	if encrypt {
		packet[1] = 1
	}
	copy(packet[2:4], token[:])
	copy(packet[4:8], sessionID[:])
	signPacket(packet)
	packet = appendTLV(packet, 3, byte(mtu>>8), byte(mtu))
	addressBytes := address.As4()
	packet = appendTLV(packet, 4, addressBytes[:]...)
	if len(dns) > 0 && dns[0].Is4() {
		dns0 := dns[0].As4()
		packet = appendTLV(packet, 5, dns0[:]...)
	}
	if len(dns) > 1 && dns[0].Is4() && dns[1].Is4() {
		dns0 := dns[0].As4()
		dns1 := dns[1].As4()
		packet = appendTLV(packet, 6, append(dns0[:], dns1[:]...)...)
	}
	if encrypt {
		packet = appendTLV(packet, 8, 1)
	}
	return packet
}

func buildOpenRejectPacket() []byte {
	packet := make([]byte, signedHeader)
	packet[0] = packetOpenReject
	signPacket(packet)
	return packet
}

func buildEchoResponsePacket(request []byte) []byte {
	length := signedHeader
	if len(request) > signedHeader {
		length = len(request)
	}
	packet := make([]byte, length)
	packet[0] = packetEchoResp
	if len(request) >= headerSize {
		packet[1] = request[1]
		copy(packet[2:4], request[2:4])
		copy(packet[4:8], request[4:8])
	}
	signPacket(packet)
	if len(request) > signedHeader {
		copy(packet[signedHeader:], request[signedHeader:])
	}
	return packet
}

func parseOpenAck(packet []byte) (openAckInfo, error) {
	if len(packet) < signedHeader {
		return openAckInfo{}, E.New("short OPENACK")
	}
	if !verifyPacket(packet) {
		return openAckInfo{}, E.New("invalid OPENACK signature")
	}
	var info openAckInfo
	info.encrypt = packet[1]
	tlvs := packet[signedHeader:]
	for len(tlvs) > 0 {
		if len(tlvs) < 2 {
			break
		}
		tlvType := tlvs[0]
		tlvLength := int(tlvs[1])
		if tlvLength < 2 || tlvLength > len(tlvs) {
			break
		}
		value := tlvs[2:tlvLength]
		switch tlvType {
		case 3:
			if len(value) >= 2 {
				info.serverMTU = binary.BigEndian.Uint16(value[:2])
			}
		case 4:
			if len(value) >= 4 {
				info.peerIP = netip.AddrFrom4([4]byte(value[:4]))
			}
		case 5:
			if len(value) >= 4 {
				info.dns0 = netip.AddrFrom4([4]byte(value[:4]))
			}
		case 6:
			if len(value) >= 4 {
				info.dns0 = netip.AddrFrom4([4]byte(value[:4]))
			}
			if len(value) >= 8 {
				info.dns1 = netip.AddrFrom4([4]byte(value[4:8]))
			}
		case 7:
			if len(value) >= 1 {
				info.peerDupPkt = value[0]
			}
		case 8:
			if len(value) >= 1 {
				info.encrypt = value[0]
			}
		}
		tlvs = tlvs[tlvLength:]
	}
	if !info.peerIP.IsValid() || info.peerIP.IsUnspecified() {
		return openAckInfo{}, E.New("OPENACK missing peer IP")
	}
	return info, nil
}

func buildEchoPacket(token [2]byte, sessionID [4]byte, curDelay uint32, minDelay uint32, maxDelay uint32, routeMagic uint32, now time.Time) []byte {
	packet := make([]byte, signedHeader+36)
	packet[0] = packetEchoReq
	copy(packet[2:4], token[:])
	copy(packet[4:8], sessionID[:])
	signPacket(packet)
	payload := packet[signedHeader:]
	binary.LittleEndian.PutUint64(payload[:8], uint64(now.UnixMicro()))
	binary.LittleEndian.PutUint32(payload[8:12], curDelay)
	binary.LittleEndian.PutUint32(payload[12:16], minDelay)
	binary.LittleEndian.PutUint32(payload[16:20], maxDelay)
	copy(payload[24:28], []byte{'T', 'D', 'R', 0})
	binary.BigEndian.PutUint32(payload[28:32], routeMagic)
	return packet
}

func buildClosePacket(token [2]byte, sessionID [4]byte) []byte {
	packet := make([]byte, signedHeader)
	packet[0] = packetClose
	copy(packet[2:4], token[:])
	copy(packet[4:8], sessionID[:])
	signPacket(packet)
	return packet
}
