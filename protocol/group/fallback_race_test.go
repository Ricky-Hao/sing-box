package group

import (
	"context"
	outboundAdapter "github.com/sagernet/sing-box/adapter/outbound"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"net"
	"sync/atomic"
)

type fallbackRaceTestOutbound struct {
	outboundAdapter.Adapter
	dialCount   atomic.Int64
	listenCount atomic.Int64
	dialStarted chan<- struct{}
	dialRelease <-chan struct{}
}

func newFallbackRaceTestOutbound(tag string) *fallbackRaceTestOutbound {
	return &fallbackRaceTestOutbound{
		Adapter: outboundAdapter.NewAdapter("test", tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
	}
}

func (o *fallbackRaceTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	if o.dialStarted != nil {
		o.dialStarted <- struct{}{}
		<-o.dialRelease
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (o *fallbackRaceTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	o.listenCount.Add(1)
	return net.ListenPacket("udp", "127.0.0.1:0")
}
