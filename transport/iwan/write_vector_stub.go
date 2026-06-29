//go:build with_gvisor && !linux

package iwan

import (
	"net"
	"net/netip"
)

func (s *Server) writeDataPacketVectorTo(conn net.PacketConn, remote netip.AddrPort, token [2]byte, sessionID [4]byte, payload []byte) error {
	return errUnsupportedVectorWrite
}

func (s *Server) readUDPConnBatchLoop(conn *net.UDPConn) bool {
	return false
}

func (s *Server) writeDataPacketVectorBatchTo(conn net.PacketConn, packets []serverOutboundPacket) (int, error) {
	return 0, errUnsupportedVectorWrite
}
