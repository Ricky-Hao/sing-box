//go:build with_gvisor && !linux

package iwan

import (
	"net"
	"net/netip"
)

func (s *Server) writeDataPacketVectorTo(conn net.PacketConn, remote netip.AddrPort, token [2]byte, sessionID [4]byte, payload []byte) error {
	return errUnsupportedVectorWrite
}
