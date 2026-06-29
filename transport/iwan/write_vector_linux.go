//go:build with_gvisor && linux

package iwan

import (
	"io"
	"net"
	"net/netip"

	"golang.org/x/sys/unix"
)

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
