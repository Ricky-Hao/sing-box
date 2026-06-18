//go:build with_iwan && with_gvisor

package iwan

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing-box/transport/iwan"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

var (
	_ adapter.OutboundWithPreferredRoutes = (*Endpoint)(nil)
	_ dialer.PacketDialerWithDestination  = (*Endpoint)(nil)
	_ adapter.DirectRouteOutbound         = (*Endpoint)(nil)
)

func RegisterEndpoint(registry *endpoint.Registry) {
	endpoint.Register[option.IWANEndpointOptions](registry, C.TypeIWAN, NewEndpoint)
}

type Endpoint struct {
	endpoint.Adapter
	ctx                context.Context
	router             adapter.Router
	dnsRouter          adapter.DNSRouter
	logger             logger.ContextLogger
	endpoint           *iwan.Endpoint
	started            atomic.Bool
	localAddressAccess sync.RWMutex
	localAddresses     []netip.Prefix
	allowedIPs         []netip.Prefix
}

func NewEndpoint(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.IWANEndpointOptions) (adapter.Endpoint, error) {
	if options.System {
		return nil, E.New("iWAN system mode is not supported yet")
	}
	if options.Server == "" {
		return nil, E.New("missing server")
	}
	if options.ServerPort == 0 {
		options.ServerPort = 4567
	}
	if options.MTU == 0 {
		options.MTU = 1400
	}
	ep := &Endpoint{
		Adapter:        endpoint.NewAdapterWithDialerOptions(C.TypeIWAN, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, options.DialerOptions),
		ctx:            ctx,
		router:         router,
		dnsRouter:      service.FromContext[adapter.DNSRouter](ctx),
		logger:         logger,
		localAddresses: options.Address,
		allowedIPs:     options.AllowedIPs,
	}
	server := options.ServerOptions.Build()
	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   server.IsDomain(),
		ResolverOnDetour: true,
	})
	if err != nil {
		return nil, err
	}
	var udpTimeout time.Duration
	if options.UDPTimeout != 0 {
		udpTimeout = time.Duration(options.UDPTimeout)
	} else {
		udpTimeout = C.UDPTimeout
	}
	iwanEndpoint, err := iwan.NewEndpoint(iwan.EndpointOptions{
		Context:         ctx,
		Logger:          logger,
		Handler:         ep,
		UDPTimeout:      udpTimeout,
		ICMPTimeout:     C.ICMPTimeout,
		Dialer:          outboundDialer,
		Server:          server,
		MTU:             options.MTU,
		ExpectedAddress: options.Address,
		Username:        options.Username,
		Password:        options.Password,
		Encrypt:         options.Encrypt,
		PipeID:          options.PipeID,
		PipeIndex:       options.PipeIndex,
		OnAddressUpdate: ep.setLocalAddresses,
	})
	if err != nil {
		return nil, err
	}
	ep.endpoint = iwanEndpoint
	return ep, nil
}

func (e *Endpoint) Start(stage adapter.StartStage) error {
	if stage == adapter.StartStatePostStart {
		err := e.endpoint.Start()
		if err != nil {
			return err
		}
		e.started.Store(true)
	}
	return nil
}

func (e *Endpoint) Close() error {
	e.started.Store(false)
	return e.endpoint.Close()
}

func (e *Endpoint) PrepareConnection(network string, source M.Socksaddr, destination M.Socksaddr, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	if !e.ready() {
		return nil, E.New("iWAN is not ready yet")
	}
	var ipVersion uint8
	if !destination.IsIPv6() {
		ipVersion = 4
	} else {
		ipVersion = 6
	}
	routeDestination, err := e.router.PreMatch(adapter.InboundContext{
		Inbound:     e.Tag(),
		InboundType: e.Type(),
		IPVersion:   ipVersion,
		Network:     network,
		Source:      source,
		Destination: destination,
	}, routeContext, timeout, false)
	if err != nil {
		switch {
		case rule.IsBypassed(err):
			err = nil
		case rule.IsRejected(err):
			e.logger.Trace("reject ", network, " connection from ", source.AddrString(), " to ", destination.AddrString())
		default:
			if network == N.NetworkICMP {
				e.logger.Warn(E.Cause(err, "link ", network, " connection from ", source.AddrString(), " to ", destination.AddrString()))
			}
		}
	}
	return routeDestination, err
}

func (e *Endpoint) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = e.Tag()
	metadata.InboundType = e.Type()
	metadata.Source = source
	for _, localPrefix := range e.getLocalAddresses() {
		if localPrefix.Contains(destination.Addr) {
			metadata.OriginDestination = destination
			if destination.Addr.Is4() {
				destination.Addr = netip.AddrFrom4([4]uint8{127, 0, 0, 1})
			} else {
				destination.Addr = netip.IPv6Loopback()
			}
			break
		}
	}
	metadata.Destination = destination
	e.logger.InfoContext(ctx, "inbound connection from ", source)
	e.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	e.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (e *Endpoint) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = e.Tag()
	metadata.InboundType = e.Type()
	metadata.Source = source
	metadata.Destination = destination
	for _, localPrefix := range e.getLocalAddresses() {
		if localPrefix.Contains(destination.Addr) {
			metadata.OriginDestination = destination
			if destination.Addr.Is4() {
				metadata.Destination.Addr = netip.AddrFrom4([4]uint8{127, 0, 0, 1})
			} else {
				metadata.Destination.Addr = netip.IPv6Loopback()
			}
			conn = bufio.NewNATPacketConn(bufio.NewNetPacketConn(conn), metadata.OriginDestination, metadata.Destination)
		}
	}
	e.logger.InfoContext(ctx, "inbound packet connection from ", source)
	e.logger.InfoContext(ctx, "inbound packet connection to ", destination)
	e.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

func (e *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch network {
	case N.NetworkTCP:
		e.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		e.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	if !e.ready() {
		return nil, E.New("iWAN is not ready yet")
	}
	if destination.IsDomain() {
		destinationAddresses, err := e.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, err
		}
		return N.DialSerial(ctx, e.endpoint, network, destination, destinationAddresses)
	} else if !destination.Addr.IsValid() {
		return nil, E.New("invalid destination: ", destination)
	}
	return e.endpoint.DialContext(ctx, network, destination)
}

func (e *Endpoint) ListenPacketWithDestination(ctx context.Context, destination M.Socksaddr) (net.PacketConn, netip.Addr, error) {
	e.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	if !e.ready() {
		return nil, netip.Addr{}, E.New("iWAN is not ready yet")
	}
	if destination.IsDomain() {
		destinationAddresses, err := e.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, netip.Addr{}, err
		}
		return N.ListenSerial(ctx, e.endpoint, destination, destinationAddresses)
	}
	packetConn, err := e.endpoint.ListenPacket(ctx, destination)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	if destination.IsIP() {
		return packetConn, destination.Addr, nil
	}
	return packetConn, netip.Addr{}, nil
}

func (e *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	packetConn, destinationAddress, err := e.ListenPacketWithDestination(ctx, destination)
	if err != nil {
		return nil, err
	}
	if destinationAddress.IsValid() && destination != M.SocksaddrFrom(destinationAddress, destination.Port) {
		return bufio.NewNATPacketConn(bufio.NewPacketConn(packetConn), M.SocksaddrFrom(destinationAddress, destination.Port), destination), nil
	}
	return packetConn, nil
}

func (e *Endpoint) PreferredDomain(domain string) bool {
	return false
}

func (e *Endpoint) PreferredAddress(address netip.Addr) bool {
	if !e.ready() {
		return false
	}
	return common.Any(e.allowedIPs, func(prefix netip.Prefix) bool {
		return prefix.Contains(address)
	})
}

func (e *Endpoint) NewDirectRouteConnection(metadata adapter.InboundContext, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	if !e.ready() {
		return nil, E.New("iWAN is not ready yet")
	}
	return e.endpoint.NewDirectRouteConnection(metadata, routeContext, timeout)
}

func (e *Endpoint) ready() bool {
	return e.started.Load() && e.endpoint.Ready()
}

func (e *Endpoint) setLocalAddresses(addresses []netip.Prefix) {
	e.localAddressAccess.Lock()
	defer e.localAddressAccess.Unlock()
	e.localAddresses = addresses
}

func (e *Endpoint) getLocalAddresses() []netip.Prefix {
	e.localAddressAccess.RLock()
	defer e.localAddressAccess.RUnlock()
	return common.Dup(e.localAddresses)
}
