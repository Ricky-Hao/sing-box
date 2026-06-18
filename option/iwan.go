package option

import (
	"net/netip"

	"github.com/sagernet/sing/common/json/badoption"
)

type IWANEndpointOptions struct {
	System     bool                             `json:"system,omitempty"`
	Address    badoption.Listable[netip.Prefix] `json:"address,omitempty"`
	AllowedIPs badoption.Listable[netip.Prefix] `json:"allowed_ips,omitempty"`
	MTU        uint32                           `json:"mtu,omitempty"`
	Username   string                           `json:"username"`
	Password   string                           `json:"password"`
	Encrypt    bool                             `json:"encrypt,omitempty"`
	PipeID     uint16                           `json:"pipe_id,omitempty"`
	PipeIndex  uint8                            `json:"pipe_index,omitempty"`
	UDPTimeout badoption.Duration               `json:"udp_timeout,omitempty"`
	ServerOptions
	DialerOptions
}
