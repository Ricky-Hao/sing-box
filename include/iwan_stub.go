//go:build !with_iwan || !with_gvisor

package include

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func registerIWANInbound(registry *inbound.Registry) {
	inbound.Register[option.IWANInboundOptions](registry, C.TypeIWAN, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.IWANInboundOptions) (adapter.Inbound, error) {
		return nil, E.New(`iWAN is not included in this build, rebuild with -tags with_iwan,with_gvisor`)
	})
}

func registerIWANEndpoint(registry *endpoint.Registry) {
	endpoint.Register[option.IWANEndpointOptions](registry, C.TypeIWAN, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.IWANEndpointOptions) (adapter.Endpoint, error) {
		return nil, E.New(`iWAN is not included in this build, rebuild with -tags with_iwan,with_gvisor`)
	})
}
