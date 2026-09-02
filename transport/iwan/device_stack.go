//go:build with_gvisor

package iwan

import (
	"context"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"

	"github.com/sagernet/gvisor/pkg/buffer"
	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/sagernet/gvisor/pkg/tcpip/header"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv4"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv6"
	"github.com/sagernet/gvisor/pkg/tcpip/stack"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/icmp"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/udp"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type stackDevice struct {
	ctx            context.Context
	logger         log.ContextLogger
	stack          *stack.Stack
	mtu            atomic.Uint32
	outbound       chan *stack.PacketBuffer
	packetOutbound chan *buf.Buffer
	done           chan struct{}
	closeOnce      sync.Once
	addressAccess  sync.RWMutex
	dispatcher     stack.NetworkDispatcher
	inet4Address   netip.Addr
	inet6Address   netip.Addr
	icmpForwarder  *tun.ICMPForwarder
	udpForwarder   *tun.UDPForwarder
}

const (
	iwanTCPBufferDefault = 8 << 20
	iwanTCPBufferMax     = 32 << 20
)

func newStackDevice(options EndpointOptions) (*stackDevice, error) {
	if options.MTU == 0 {
		options.MTU = defaultMTU
	}
	device := &stackDevice{
		ctx:            options.Context,
		logger:         options.Logger,
		outbound:       make(chan *stack.PacketBuffer, linkQueueSize),
		packetOutbound: make(chan *buf.Buffer, linkQueueSize),
		done:           make(chan struct{}),
	}
	device.mtu.Store(options.MTU)
	ipStack, err := tun.NewGVisorStackWithOptions((*iwanLinkEndpoint)(device), stack.NICOptions{}, true)
	if err != nil {
		return nil, err
	}
	device.stack = ipStack
	if err = configureStackTCP(ipStack); err != nil {
		return nil, err
	}
	if options.Handler != nil {
		ipStack.SetTransportProtocolHandler(tcp.ProtocolNumber, newIwanTCPForwarder(options.Context, ipStack, options.Handler).HandlePacket)
		udpForwarder := tun.NewUDPForwarder(options.Context, ipStack, options.Handler, tun.UDPNatOptions{
			Timeout: options.UDPTimeout,
			Shared:  true,
		})
		ipStack.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)
		device.udpForwarder = udpForwarder
		icmpForwarder := tun.NewICMPForwarder(ipStack, options.Handler, options.Logger)
		ipStack.SetTransportProtocolHandler(icmp.ProtocolNumber4, icmpForwarder.HandlePacket)
		ipStack.SetTransportProtocolHandler(icmp.ProtocolNumber6, icmpForwarder.HandlePacket)
		device.icmpForwarder = icmpForwarder
	}
	return device, nil
}

func configureStackTCP(ipStack *stack.Stack) error {
	sendBuffer := tcpip.TCPSendBufferSizeRangeOption{
		Min:     tcp.MinBufferSize,
		Default: iwanTCPBufferDefault,
		Max:     iwanTCPBufferMax,
	}
	if err := ipStack.SetTransportProtocolOption(tcp.ProtocolNumber, &sendBuffer); err != nil {
		return E.New("set iWAN TCP send buffer size: ", err.String())
	}
	receiveBuffer := tcpip.TCPReceiveBufferSizeRangeOption{
		Min:     tcp.MinBufferSize,
		Default: iwanTCPBufferDefault,
		Max:     iwanTCPBufferMax,
	}
	if err := ipStack.SetTransportProtocolOption(tcp.ProtocolNumber, &receiveBuffer); err != nil {
		return E.New("set iWAN TCP receive buffer size: ", err.String())
	}
	congestionControl := tcpip.CongestionControlOption("cubic")
	if err := ipStack.SetTransportProtocolOption(tcp.ProtocolNumber, &congestionControl); err != nil {
		return E.New("set iWAN TCP congestion control: ", err.String())
	}
	return nil
}

func (d *stackDevice) Start() error {
	if d.udpForwarder != nil {
		return d.udpForwarder.Start()
	}
	return nil
}

func (d *stackDevice) SetLocalAddress(prefix netip.Prefix) error {
	addr := tun.AddressFromAddr(prefix.Addr())
	protoAddr := tcpip.ProtocolAddress{
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   addr,
			PrefixLen: prefix.Bits(),
		},
	}
	d.addressAccess.Lock()
	defer d.addressAccess.Unlock()
	if prefix.Addr().Is4() {
		if d.inet4Address == prefix.Addr() {
			return nil
		}
		protoAddr.Protocol = ipv4.ProtocolNumber
	} else {
		if d.inet6Address == prefix.Addr() {
			return nil
		}
		protoAddr.Protocol = ipv6.ProtocolNumber
	}
	var previousAddress netip.Addr
	if prefix.Addr().Is4() {
		previousAddress = d.inet4Address
	} else {
		previousAddress = d.inet6Address
	}
	gErr := d.stack.AddProtocolAddress(tun.DefaultNIC, protoAddr, stack.AddressProperties{})
	addedAddress := true
	if gErr != nil {
		if _, ok := gErr.(*tcpip.ErrDuplicateAddress); !ok {
			return E.New("add iWAN local address ", protoAddr.AddressWithPrefix, ": ", gErr.String())
		}
		addedAddress = false
	}
	if previousAddress.IsValid() {
		oldAddr := tun.AddressFromAddr(previousAddress)
		if oldAddr != addr {
			removeErr := d.stack.RemoveAddress(tun.DefaultNIC, oldAddr)
			if removeErr != nil {
				if _, ok := removeErr.(*tcpip.ErrBadLocalAddress); !ok {
					if addedAddress {
						_ = d.stack.RemoveAddress(tun.DefaultNIC, addr)
					}
					return E.New("remove previous iWAN local address ", oldAddr, ": ", removeErr.String())
				}
			}
		}
	}
	if prefix.Addr().Is4() {
		d.inet4Address = prefix.Addr()
	} else {
		d.inet6Address = prefix.Addr()
	}
	return nil
}

func (d *stackDevice) SetMTU(mtu uint32) {
	d.mtu.Store(mtu)
}

func (d *stackDevice) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	addr := tcpip.FullAddress{
		NIC:  tun.DefaultNIC,
		Port: destination.Port,
		Addr: tun.AddressFromAddr(destination.Addr),
	}
	bind := tcpip.FullAddress{
		NIC: tun.DefaultNIC,
	}
	var networkProtocol tcpip.NetworkProtocolNumber
	d.addressAccess.RLock()
	if destination.IsIPv4() {
		if !d.inet4Address.IsValid() {
			d.addressAccess.RUnlock()
			return nil, E.New("missing iWAN IPv4 local address")
		}
		networkProtocol = header.IPv4ProtocolNumber
		bind.Addr = tun.AddressFromAddr(d.inet4Address)
	} else {
		if !d.inet6Address.IsValid() {
			d.addressAccess.RUnlock()
			return nil, E.New("missing iWAN IPv6 local address")
		}
		networkProtocol = header.IPv6ProtocolNumber
		bind.Addr = tun.AddressFromAddr(d.inet6Address)
	}
	d.addressAccess.RUnlock()
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		return DialTCPWithBind(ctx, d.stack, bind, addr, networkProtocol)
	case N.NetworkUDP:
		return gonet.DialUDP(d.stack, &bind, &addr, networkProtocol)
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (d *stackDevice) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	bind := tcpip.FullAddress{
		NIC: tun.DefaultNIC,
	}
	var networkProtocol tcpip.NetworkProtocolNumber
	d.addressAccess.RLock()
	if destination.IsIPv4() {
		if !d.inet4Address.IsValid() {
			d.addressAccess.RUnlock()
			return nil, E.New("missing iWAN IPv4 local address")
		}
		networkProtocol = header.IPv4ProtocolNumber
		bind.Addr = tun.AddressFromAddr(d.inet4Address)
	} else {
		if !d.inet6Address.IsValid() {
			d.addressAccess.RUnlock()
			return nil, E.New("missing iWAN IPv6 local address")
		}
		networkProtocol = header.IPv6ProtocolNumber
		bind.Addr = tun.AddressFromAddr(d.inet6Address)
	}
	d.addressAccess.RUnlock()
	return gonet.DialUDP(d.stack, &bind, nil, networkProtocol)
}

func (d *stackDevice) Read(packet []byte) (int, error) {
	select {
	case packetBuffer, ok := <-d.outbound:
		if !ok {
			return 0, os.ErrClosed
		}
		defer packetBuffer.DecRef()
		return copyPacketBuffer(packet, packetBuffer), nil
	case packetBuffer := <-d.packetOutbound:
		defer packetBuffer.Release()
		return copy(packet, packetBuffer.Bytes()), nil
	case <-d.done:
		return 0, os.ErrClosed
	}
}

func (d *stackDevice) ReadBatch(packets [][]byte, packetSizes []int) (int, error) {
	if len(packets) == 0 {
		return 0, nil
	}
	n, err := d.Read(packets[0])
	if err != nil {
		return 0, err
	}
	packetSizes[0] = n
	packetCount := 1
	for packetCount < len(packets) {
		select {
		case packetBuffer, ok := <-d.outbound:
			if !ok {
				return packetCount, nil
			}
			packetSizes[packetCount] = copyPacketBuffer(packets[packetCount], packetBuffer)
			packetBuffer.DecRef()
		case packetBuffer := <-d.packetOutbound:
			packetSizes[packetCount] = copy(packets[packetCount], packetBuffer.Bytes())
			packetBuffer.Release()
		case <-d.done:
			return packetCount, nil
		default:
			return packetCount, nil
		}
		packetCount++
	}
	return packetCount, nil
}

func (d *stackDevice) ReadPayloadBatch(payloads []serverOutboundPayload) (int, error) {
	if len(payloads) == 0 {
		return 0, nil
	}
	if err := d.readPayload(&payloads[0]); err != nil {
		return 0, err
	}
	payloadCount := 1
	for payloadCount < len(payloads) {
		select {
		case packetBuffer, ok := <-d.outbound:
			if !ok {
				return payloadCount, nil
			}
			payloads[payloadCount].setPacketBuffer(packetBuffer)
		case packetBuffer := <-d.packetOutbound:
			payloads[payloadCount].setBuffer(packetBuffer)
		case <-d.done:
			return payloadCount, nil
		default:
			return payloadCount, nil
		}
		payloadCount++
	}
	return payloadCount, nil
}

func (d *stackDevice) readPayload(payload *serverOutboundPayload) error {
	select {
	case packetBuffer, ok := <-d.outbound:
		if !ok {
			return os.ErrClosed
		}
		payload.setPacketBuffer(packetBuffer)
		return nil
	case packetBuffer := <-d.packetOutbound:
		payload.setBuffer(packetBuffer)
		return nil
	case <-d.done:
		return os.ErrClosed
	}
}

type serverOutboundPayload struct {
	views        [][]byte
	size         int
	packetBuffer *stack.PacketBuffer
	buffer       *buf.Buffer
}

func (p *serverOutboundPayload) setPacketBuffer(packetBuffer *stack.PacketBuffer) {
	p.release()
	p.views = packetBuffer.AsSlices()
	p.size = packetBuffer.Size()
	p.packetBuffer = packetBuffer
}

func (p *serverOutboundPayload) setBuffer(packetBuffer *buf.Buffer) {
	p.release()
	payload := packetBuffer.Bytes()
	p.views = [][]byte{payload}
	p.size = len(payload)
	p.buffer = packetBuffer
}

func (p *serverOutboundPayload) release() {
	if p.packetBuffer != nil {
		p.packetBuffer.DecRef()
	}
	if p.buffer != nil {
		p.buffer.Release()
	}
	p.views = nil
	p.size = 0
	p.packetBuffer = nil
	p.buffer = nil
}

func (p *serverOutboundPayload) destination() (netip.Addr, bool) {
	if len(p.views) == 0 {
		return netip.Addr{}, false
	}
	if len(p.views) == 1 {
		return ipv4Destination(p.views[0])
	}
	var headerBuffer [20]byte
	var headerSize int
	for _, view := range p.views {
		headerSize += copy(headerBuffer[headerSize:], view)
		if headerSize == len(headerBuffer) {
			break
		}
	}
	if headerSize < len(headerBuffer) || headerBuffer[0]>>4 != 4 {
		return netip.Addr{}, false
	}
	ipHeaderLength := int(headerBuffer[0]&0x0f) * 4
	if ipHeaderLength < 20 || p.size < ipHeaderLength {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(headerBuffer[16:20])), true
}

func copyPacketBuffer(packet []byte, packetBuffer *stack.PacketBuffer) int {
	var n int
	for _, view := range packetBuffer.AsSlices() {
		n += copy(packet[n:], view)
	}
	return n
}

func (d *stackDevice) Write(packet []byte) error {
	if len(packet) == 0 {
		return nil
	}
	var networkProtocol tcpip.NetworkProtocolNumber
	switch header.IPVersion(packet) {
	case header.IPv4Version:
		networkProtocol = header.IPv4ProtocolNumber
	case header.IPv6Version:
		networkProtocol = header.IPv6ProtocolNumber
	default:
		return nil
	}
	view := buffer.NewViewSize(len(packet))
	copy(view.AsSlice(), packet)
	packetBuffer := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithView(view),
	})
	d.dispatcher.DeliverNetworkPacket(networkProtocol, packetBuffer)
	packetBuffer.DecRef()
	return nil
}

func (d *stackDevice) Close() error {
	d.closeOnce.Do(func() {
		close(d.done)
		if d.udpForwarder != nil {
			_ = d.udpForwarder.Close()
		}
		if d.icmpForwarder != nil {
			_ = d.icmpForwarder.Close()
		}
		if d.stack != nil {
			d.stack.Close()
			for _, endpoint := range d.stack.CleanupEndpoints() {
				endpoint.Abort()
			}
			d.stack.Wait()
		}
	})
	return nil
}

var _ stack.LinkEndpoint = (*iwanLinkEndpoint)(nil)

type iwanLinkEndpoint stackDevice

func (ep *iwanLinkEndpoint) MTU() uint32 {
	return ep.mtu.Load()
}

func (ep *iwanLinkEndpoint) SetMTU(mtu uint32) {
	ep.mtu.Store(mtu)
}

func (ep *iwanLinkEndpoint) MaxHeaderLength() uint16 {
	return 0
}

func (ep *iwanLinkEndpoint) LinkAddress() tcpip.LinkAddress {
	return ""
}

func (ep *iwanLinkEndpoint) SetLinkAddress(addr tcpip.LinkAddress) {
}

func (ep *iwanLinkEndpoint) Capabilities() stack.LinkEndpointCapabilities {
	return stack.CapabilityRXChecksumOffload
}

func (ep *iwanLinkEndpoint) Attach(dispatcher stack.NetworkDispatcher) {
	ep.dispatcher = dispatcher
}

func (ep *iwanLinkEndpoint) IsAttached() bool {
	return ep.dispatcher != nil
}

func (ep *iwanLinkEndpoint) Wait() {
}

func (ep *iwanLinkEndpoint) ARPHardwareType() header.ARPHardwareType {
	return header.ARPHardwareNone
}

func (ep *iwanLinkEndpoint) AddHeader(buffer *stack.PacketBuffer) {
}

func (ep *iwanLinkEndpoint) ParseHeader(ptr *stack.PacketBuffer) bool {
	return true
}

func (ep *iwanLinkEndpoint) WritePackets(list stack.PacketBufferList) (int, tcpip.Error) {
	for _, packetBuffer := range list.AsSlice() {
		packetBuffer.IncRef()
		select {
		case <-ep.done:
			return 0, &tcpip.ErrClosedForSend{}
		case ep.outbound <- packetBuffer:
		}
	}
	return list.Len(), nil
}

func (ep *iwanLinkEndpoint) Close() {
}

func (ep *iwanLinkEndpoint) SetOnCloseAction(f func()) {
}
