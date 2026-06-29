//go:build with_gvisor && linux

package iwan

import (
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strconv"
	"unsafe"

	E "github.com/sagernet/sing/common/exceptions"
	"golang.org/x/sys/unix"
)

type mmsghdr struct {
	header unix.Msghdr
	length uint32
}

func (s *Server) writeDataPacketVectorTo(conn net.PacketConn, remote netip.AddrPort, token [2]byte, sessionID [4]byte, payload []byte) error {
	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		return errUnsupportedVectorWrite
	}
	rawConn, err := udpConn.SyscallConn()
	if err != nil {
		return err
	}
	sockaddr, err := udpSockaddr(remote)
	if err != nil {
		return err
	}
	var header [headerSize]byte
	fillDataPacketHeader(header[:], token, sessionID, false)
	var writeErr error
	var n int
	s.writeAccess.Lock()
	defer s.writeAccess.Unlock()
	err = rawConn.Write(func(fd uintptr) bool {
		for {
			n, writeErr = unix.SendmsgBuffers(int(fd), [][]byte{header[:], payload}, nil, sockaddr, 0)
			if writeErr != unix.EINTR {
				break
			}
		}
		return writeErr != unix.EAGAIN && writeErr != unix.EWOULDBLOCK
	})
	if err != nil {
		return err
	}
	if writeErr != nil {
		return writeErr
	}
	if n != len(header)+len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

type udpReceiveBatch struct {
	buffers  [udpBatchSize][]byte
	iovecs   [udpBatchSize]unix.Iovec
	names    [udpBatchSize]unix.RawSockaddrAny
	messages [udpBatchSize]mmsghdr
}

func newUDPReceiveBatch() *udpReceiveBatch {
	batch := &udpReceiveBatch{}
	for index := range batch.buffers {
		batch.buffers[index] = make([]byte, fragmentOutputSize)
	}
	return batch
}

func (b *udpReceiveBatch) prepare() {
	for index := range b.messages {
		b.iovecs[index].Base = &b.buffers[index][0]
		b.iovecs[index].SetLen(len(b.buffers[index]))
		b.messages[index] = mmsghdr{}
		b.messages[index].header.Name = (*byte)(unsafe.Pointer(&b.names[index]))
		b.messages[index].header.Namelen = unix.SizeofSockaddrAny
		b.messages[index].header.Iov = &b.iovecs[index]
		b.messages[index].header.Iovlen = 1
	}
}

func (b *udpReceiveBatch) read(rawConn syscallRawConn) (int, error) {
	b.prepare()
	var packetCount int
	var readErr error
	err := rawConn.Read(func(fd uintptr) bool {
		for {
			packetCount, readErr = recvmmsg(int(fd), b.messages[:])
			if readErr != unix.EINTR {
				break
			}
		}
		return readErr != unix.EAGAIN && readErr != unix.EWOULDBLOCK
	})
	if err != nil {
		return 0, err
	}
	if readErr != nil {
		return 0, readErr
	}
	return packetCount, nil
}

func (s *Server) readUDPConnBatchLoop(conn *net.UDPConn) bool {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	batch := newUDPReceiveBatch()
	for {
		packetCount, err := batch.read(rawConn)
		if err != nil {
			if err == unix.ENOSYS || err == unix.EINVAL {
				return false
			}
			select {
			case <-s.ctx.Done():
				return true
			default:
			}
			s.options.Logger.Error(E.Cause(err, "read iWAN server packet"))
			return true
		}
		for index := 0; index < packetCount; index++ {
			remote, ok := rawSockaddrAddrPort(&batch.names[index])
			if !ok {
				continue
			}
			_ = s.handlePacket(conn, remote, batch.buffers[index][:batch.messages[index].length])
		}
	}
}

func (s *Server) writeDataPacketVectorBatchTo(conn net.PacketConn, packets []serverOutboundPacket) (int, error) {
	udpConn, ok := conn.(*net.UDPConn)
	if !ok || len(packets) == 0 {
		return 0, errUnsupportedVectorWrite
	}
	for index := range packets {
		if packets[index].encrypt {
			return 0, errUnsupportedVectorWrite
		}
	}
	rawConn, err := udpConn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var headers [udpBatchSize][headerSize]byte
	var names [udpBatchSize]unix.RawSockaddrAny
	var iovecs [udpBatchSize][2]unix.Iovec
	var messages [udpBatchSize]mmsghdr
	messageCount := min(len(packets), udpBatchSize)
	for index := 0; index < messageCount; index++ {
		nameLength, ok := fillRawSockaddrAddrPort(&names[index], packets[index].remote)
		if !ok {
			return 0, errUnsupportedVectorWrite
		}
		fillDataPacketHeader(headers[index][:], packets[index].token, packets[index].sessionID, false)
		iovecs[index][0].Base = &headers[index][0]
		iovecs[index][0].SetLen(len(headers[index]))
		iovecs[index][1].Base = &packets[index].payload[0]
		iovecs[index][1].SetLen(len(packets[index].payload))
		messages[index].header.Name = (*byte)(unsafe.Pointer(&names[index]))
		messages[index].header.Namelen = nameLength
		messages[index].header.Iov = &iovecs[index][0]
		messages[index].header.Iovlen = 2
	}
	var sent int
	var writeErr error
	s.writeAccess.Lock()
	defer s.writeAccess.Unlock()
	err = rawConn.Write(func(fd uintptr) bool {
		for {
			sent, writeErr = sendmmsg(int(fd), messages[:messageCount])
			if writeErr != unix.EINTR {
				break
			}
		}
		return writeErr != unix.EAGAIN && writeErr != unix.EWOULDBLOCK
	})
	if err != nil {
		return sent, err
	}
	if sent == 0 && (writeErr == unix.ENOSYS || writeErr == unix.EINVAL) {
		return 0, errUnsupportedVectorWrite
	}
	return sent, writeErr
}

func udpSockaddr(remote netip.AddrPort) (unix.Sockaddr, error) {
	address := remote.Addr()
	if address.Is4() {
		address4 := address.As4()
		return &unix.SockaddrInet4{
			Port: int(remote.Port()),
			Addr: address4,
		}, nil
	}
	if address.Is6() {
		if address.Zone() != "" {
			return nil, errUnsupportedVectorWrite
		}
		address6 := address.As16()
		return &unix.SockaddrInet6{
			Port: int(remote.Port()),
			Addr: address6,
		}, nil
	}
	return nil, errUnsupportedVectorWrite
}

type syscallRawConn interface {
	Read(func(fd uintptr) bool) error
	Write(func(fd uintptr) bool) error
}

func recvmmsg(fd int, messages []mmsghdr) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	n, _, errno := unix.Syscall6(unix.SYS_RECVMMSG, uintptr(fd), uintptr(unsafe.Pointer(&messages[0])), uintptr(len(messages)), uintptr(unix.MSG_DONTWAIT), 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func sendmmsg(fd int, messages []mmsghdr) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	n, _, errno := unix.Syscall6(unix.SYS_SENDMMSG, uintptr(fd), uintptr(unsafe.Pointer(&messages[0])), uintptr(len(messages)), 0, 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func rawSockaddrAddrPort(raw *unix.RawSockaddrAny) (netip.AddrPort, bool) {
	switch raw.Addr.Family {
	case unix.AF_INET:
		raw4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(raw))
		return netip.AddrPortFrom(netip.AddrFrom4(raw4.Addr), ntohs(raw4.Port)), true
	case unix.AF_INET6:
		raw6 := (*unix.RawSockaddrInet6)(unsafe.Pointer(raw))
		address := netip.AddrFrom16(raw6.Addr)
		if raw6.Scope_id != 0 {
			address = address.WithZone(strconv.FormatUint(uint64(raw6.Scope_id), 10))
		}
		return netip.AddrPortFrom(address, ntohs(raw6.Port)), true
	default:
		return netip.AddrPort{}, false
	}
}

func fillRawSockaddrAddrPort(raw *unix.RawSockaddrAny, remote netip.AddrPort) (uint32, bool) {
	address := remote.Addr()
	if address.Is4() {
		raw4 := (*unix.RawSockaddrInet4)(unsafe.Pointer(raw))
		*raw4 = unix.RawSockaddrInet4{
			Family: unix.AF_INET,
			Addr:   address.As4(),
		}
		setNtohs(&raw4.Port, remote.Port())
		return unix.SizeofSockaddrInet4, true
	}
	if address.Is6() {
		if address.Zone() != "" {
			return 0, false
		}
		raw6 := (*unix.RawSockaddrInet6)(unsafe.Pointer(raw))
		*raw6 = unix.RawSockaddrInet6{
			Family: unix.AF_INET6,
			Addr:   address.As16(),
		}
		setNtohs(&raw6.Port, remote.Port())
		return unix.SizeofSockaddrInet6, true
	}
	return 0, false
}

func ntohs(port uint16) uint16 {
	return binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&port))[:])
}

func setNtohs(port *uint16, value uint16) {
	binary.BigEndian.PutUint16((*[2]byte)(unsafe.Pointer(port))[:], value)
}
