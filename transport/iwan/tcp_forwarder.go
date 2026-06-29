//go:build with_gvisor

package iwan

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/sagernet/gvisor/pkg/tcpip/stack"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
	"github.com/sagernet/gvisor/pkg/waiter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type iwanTCPForwarder struct {
	ctx       context.Context
	handler   tun.Handler
	forwarder *tcp.Forwarder
}

func newIwanTCPForwarder(ctx context.Context, ipStack *stack.Stack, handler tun.Handler) *iwanTCPForwarder {
	forwarder := &iwanTCPForwarder{
		ctx:     ctx,
		handler: handler,
	}
	forwarder.forwarder = tcp.NewForwarder(ipStack, iwanTCPBufferDefault, 1024, forwarder.forward)
	return forwarder
}

func (f *iwanTCPForwarder) HandlePacket(id stack.TransportEndpointID, packetBuffer *stack.PacketBuffer) bool {
	return f.forwarder.HandlePacket(id, packetBuffer)
}

func (f *iwanTCPForwarder) forward(request *tcp.ForwarderRequest) {
	source := M.SocksaddrFrom(tun.AddrFromAddress(request.ID().RemoteAddress), request.ID().RemotePort)
	destination := M.SocksaddrFrom(tun.AddrFromAddress(request.ID().LocalAddress), request.ID().LocalPort)
	_, err := f.handler.PrepareConnection(N.NetworkTCP, source, destination, nil, 0)
	if err != nil {
		request.Complete(!errors.Is(err, tun.ErrDrop))
		return
	}
	conn := &iwanLazyTCPConn{
		parentCtx:  f.ctx,
		request:    request,
		localAddr:  source.TCPAddr(),
		remoteAddr: destination.TCPAddr(),
	}
	go f.handler.NewConnectionEx(f.ctx, conn, source, destination, nil)
}

type iwanLazyTCPConn struct {
	tcpConn         *gonet.TCPConn
	parentCtx       context.Context
	request         *tcp.ForwarderRequest
	localAddr       net.Addr
	remoteAddr      net.Addr
	handshakeAccess sync.Mutex
	handshakeDone   bool
	handshakeErr    error
}

func (c *iwanLazyTCPConn) HandshakeContext(ctx context.Context) error {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	if c.handshakeDone {
		return c.handshakeErr
	}
	var waitQueue waiter.Queue
	handshakeCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-c.parentCtx.Done():
			waitQueue.Notify(waitQueue.Events())
		case <-handshakeCtx.Done():
		}
	}()
	endpoint, tcpErr := c.request.CreateEndpoint(&waitQueue)
	cancel()
	if tcpErr != nil {
		err := gonet.TranslateNetstackError(tcpErr)
		c.handshakeErr = err
		c.handshakeDone = true
		c.request.Complete(true)
		return err
	}
	c.request.Complete(false)
	endpoint.SocketOptions().SetKeepAlive(true)
	endpoint.SocketOptions().SetReceiveBufferSize(iwanTCPBufferDefault, false)
	endpoint.SocketOptions().SetSendBufferSize(iwanTCPBufferDefault, false)
	_ = endpoint.SetSockOpt(common.Ptr(tcpip.KeepaliveIdleOption(15 * time.Second)))
	_ = endpoint.SetSockOpt(common.Ptr(tcpip.KeepaliveIntervalOption(15 * time.Second)))
	c.tcpConn = gonet.NewTCPConn(&waitQueue, endpoint)
	c.handshakeDone = true
	return nil
}

func (c *iwanLazyTCPConn) HandshakeFailure(err error) error {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	if c.handshakeDone {
		return net.ErrClosed
	}
	c.request.Complete(!errors.Is(err, tun.ErrDrop))
	c.handshakeErr = err
	c.handshakeDone = true
	return nil
}

func (c *iwanLazyTCPConn) HandshakeSuccess() error {
	return c.HandshakeContext(context.Background())
}

func (c *iwanLazyTCPConn) Read(b []byte) (int, error) {
	tcpConn, err := c.tcpConnection(context.Background())
	if err != nil {
		return 0, err
	}
	return tcpConn.Read(b)
}

func (c *iwanLazyTCPConn) Write(b []byte) (int, error) {
	tcpConn, err := c.tcpConnection(context.Background())
	if err != nil {
		return 0, err
	}
	return tcpConn.Write(b)
}

func (c *iwanLazyTCPConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *iwanLazyTCPConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *iwanLazyTCPConn) SetDeadline(t time.Time) error {
	tcpConn, err := c.tcpConnection(context.Background())
	if err != nil {
		return err
	}
	return tcpConn.SetDeadline(t)
}

func (c *iwanLazyTCPConn) SetReadDeadline(t time.Time) error {
	tcpConn, err := c.tcpConnection(context.Background())
	if err != nil {
		return err
	}
	return tcpConn.SetReadDeadline(t)
}

func (c *iwanLazyTCPConn) SetWriteDeadline(t time.Time) error {
	tcpConn, err := c.tcpConnection(context.Background())
	if err != nil {
		return err
	}
	return tcpConn.SetWriteDeadline(t)
}

func (c *iwanLazyTCPConn) Close() error {
	tcpConn, ok := c.closeReadyTCPConn()
	if !ok {
		return nil
	}
	return tcpConn.Close()
}

func (c *iwanLazyTCPConn) CloseRead() error {
	tcpConn, ok := c.closeReadyTCPConn()
	if !ok {
		return nil
	}
	return tcpConn.CloseRead()
}

func (c *iwanLazyTCPConn) CloseWrite() error {
	tcpConn, ok := c.closeReadyTCPConn()
	if !ok {
		return nil
	}
	return tcpConn.CloseWrite()
}

func (c *iwanLazyTCPConn) tcpConnection(ctx context.Context) (*gonet.TCPConn, error) {
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	if c.handshakeErr != nil {
		return nil, c.handshakeErr
	}
	if c.tcpConn == nil {
		return nil, net.ErrClosed
	}
	return c.tcpConn, nil
}

func (c *iwanLazyTCPConn) closeReadyTCPConn() (*gonet.TCPConn, bool) {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	if !c.handshakeDone {
		c.request.Complete(true)
		c.handshakeErr = net.ErrClosed
		c.handshakeDone = true
		return nil, false
	}
	if c.handshakeErr != nil {
		return nil, false
	}
	if c.tcpConn == nil {
		return nil, false
	}
	return c.tcpConn, true
}

func (c *iwanLazyTCPConn) ReaderReplaceable() bool {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	return c.handshakeDone && c.handshakeErr == nil
}

func (c *iwanLazyTCPConn) WriterReplaceable() bool {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	return c.handshakeDone && c.handshakeErr == nil
}

func (c *iwanLazyTCPConn) Upstream() any {
	c.handshakeAccess.Lock()
	defer c.handshakeAccess.Unlock()
	return c.tcpConn
}
