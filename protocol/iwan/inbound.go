//go:build with_iwan && with_gvisor

package iwan

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing-box/transport/iwan"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.IWANInboundOptions](registry, C.TypeIWAN, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	ctx      context.Context
	router   adapter.Router
	logger   log.ContextLogger
	listener *listener.Listener
	server   *iwan.Server
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.IWANInboundOptions) (adapter.Inbound, error) {
	options.UDPFragmentDefault = true
	if options.ListenPort == 0 {
		options.ListenPort = 4567
	}
	if options.MTU == 0 {
		options.MTU = 1400
	}
	var users []iwan.ServerUser
	if options.Username != "" || options.Password != "" {
		if options.Username == "" || options.Password == "" {
			return nil, E.New("username and password must be configured together")
		}
		if len(options.Users) > 0 {
			return nil, E.New("username/password is conflict with users")
		}
		users = append(users, iwan.ServerUser{
			Username: options.Username,
			Password: options.Password,
		})
	} else {
		users = common.Map(options.Users, func(it option.IWANInboundUser) iwan.ServerUser {
			return iwan.ServerUser{
				Username: it.Username,
				Password: it.Password,
				Address:  it.Address,
			}
		})
	}
	dns := make([]netip.Addr, 0, len(options.DNS))
	for _, address := range options.DNS {
		if !address.Is4() {
			return nil, E.New("iWAN DNS server must be IPv4: ", address)
		}
		if len(dns) == 2 {
			return nil, E.New("iWAN supports at most two DNS servers")
		}
		dns = append(dns, address)
	}
	var udpTimeout time.Duration
	if options.UDPTimeout != 0 {
		udpTimeout = time.Duration(options.UDPTimeout)
	} else {
		udpTimeout = C.UDPTimeout
	}
	var sessionTimeout time.Duration
	if options.SessionTimeout != 0 {
		sessionTimeout = time.Duration(options.SessionTimeout)
	}
	var networkManager adapter.NetworkManager
	if options.System {
		networkManager = service.FromContext[adapter.NetworkManager](ctx)
		if networkManager == nil {
			return nil, E.New("missing network manager for iWAN system mode")
		}
		if networkManager.InterfaceMonitor() == nil {
			return nil, E.New("missing interface monitor for iWAN system mode")
		}
	}
	i := &Inbound{
		Adapter: inbound.NewAdapter(C.TypeIWAN, tag),
		ctx:     ctx,
		router:  router,
		logger:  logger,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Listen:  options.ListenOptions,
		}),
	}
	serverOptions := iwan.ServerOptions{
		Context:        ctx,
		Logger:         logger,
		Handler:        i,
		UDPTimeout:     udpTimeout,
		ICMPTimeout:    C.ICMPTimeout,
		AddressPool:    options.AddressPool,
		Users:          users,
		MTU:            options.MTU,
		Encrypt:        options.Encrypt,
		DNS:            dns,
		SessionTimeout: sessionTimeout,
		System:         options.System,
		InterfaceName:  options.InterfaceName,
	}
	if options.System {
		serverOptions.InterfaceMonitor = networkManager.InterfaceMonitor()
		serverOptions.InterfaceFinder = networkManager.InterfaceFinder()
	}
	server, err := iwan.NewServer(serverOptions)
	if err != nil {
		return nil, err
	}
	i.server = server
	return i, nil
}

func (i *Inbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	packetConn, err := i.listener.ListenUDP()
	if err != nil {
		return err
	}
	return i.server.Start(packetConn)
}

func (i *Inbound) Close() error {
	return common.Close(
		i.server,
		i.listener,
	)
}

func (i *Inbound) PrepareConnection(network string, source M.Socksaddr, destination M.Socksaddr, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	var ipVersion uint8
	if !destination.IsIPv6() {
		ipVersion = 4
	} else {
		ipVersion = 6
	}
	metadata := adapter.InboundContext{
		Inbound:     i.Tag(),
		InboundType: i.Type(),
		IPVersion:   ipVersion,
		Network:     network,
		Source:      source,
		Destination: destination,
	}
	if source.Addr.IsValid() {
		metadata.User = i.server.UserByAddress(source.Addr)
	}
	routeDestination, err := i.router.PreMatch(metadata, routeContext, timeout, false)
	if err != nil {
		switch {
		case rule.IsBypassed(err):
			err = nil
		case rule.IsRejected(err):
			i.logger.Trace("reject ", network, " connection from ", source.AddrString(), " to ", destination.AddrString())
		default:
			if network == N.NetworkICMP {
				i.logger.Warn(E.Cause(err, "link ", network, " connection from ", source.AddrString(), " to ", destination.AddrString()))
			}
		}
	}
	return routeDestination, err
}

func (i *Inbound) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	ctx = log.ContextWithNewID(ctx)
	var metadata adapter.InboundContext
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	metadata.Source = source
	metadata.Destination = destination
	if source.Addr.IsValid() {
		metadata.User = i.server.UserByAddress(source.Addr)
	}
	i.logger.InfoContext(ctx, "inbound connection from ", metadata.Source)
	i.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	i.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (i *Inbound) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	ctx = log.ContextWithNewID(ctx)
	var metadata adapter.InboundContext
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	metadata.Source = source
	metadata.Destination = destination
	if source.Addr.IsValid() {
		metadata.User = i.server.UserByAddress(source.Addr)
	}
	i.logger.InfoContext(ctx, "inbound packet connection from ", metadata.Source)
	i.logger.InfoContext(ctx, "inbound packet connection to ", metadata.Destination)
	i.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}
