//go:build with_gvisor

package iwan

import "github.com/sagernet/sing/common/logger"

type udpBufferConn interface {
	SetReadBuffer(bytes int) error
	SetWriteBuffer(bytes int) error
}

func setUDPSocketBuffer(log logger.ContextLogger, conn any) {
	bufferConn, ok := conn.(udpBufferConn)
	if !ok {
		return
	}
	if err := bufferConn.SetReadBuffer(udpSocketBuffer); err != nil {
		log.Debug("set iWAN UDP read buffer: ", err)
	}
	if err := bufferConn.SetWriteBuffer(udpSocketBuffer); err != nil {
		log.Debug("set iWAN UDP write buffer: ", err)
	}
}
