package iwan

import (
	"context"
	"net/netip"
	"time"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type EndpointOptions struct {
	Context         context.Context
	Logger          logger.ContextLogger
	Handler         tun.Handler
	UDPTimeout      time.Duration
	ICMPTimeout     time.Duration
	Dialer          N.Dialer
	Server          M.Socksaddr
	MTU             uint32
	ExpectedAddress []netip.Prefix
	Username        string
	Password        string
	Encrypt         bool
	PipeID          uint16
	PipeIndex       uint8
	OnAddressUpdate func([]netip.Prefix)
}
