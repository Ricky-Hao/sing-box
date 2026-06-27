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

type IWANInboundOptions struct {
	ListenOptions
	System         bool                           `json:"system,omitempty"`
	InterfaceName  string                         `json:"interface_name,omitempty"`
	AddressPool    netip.Prefix                   `json:"address_pool"`
	Username       string                         `json:"username,omitempty"`
	Password       string                         `json:"password,omitempty"`
	Users          []IWANInboundUser              `json:"users,omitempty"`
	MTU            uint32                         `json:"mtu,omitempty"`
	Encrypt        bool                           `json:"encrypt,omitempty"`
	DNS            badoption.Listable[netip.Addr] `json:"dns,omitempty"`
	SessionTimeout badoption.Duration             `json:"session_timeout,omitempty"`
}

type IWANInboundUser struct {
	Username string     `json:"username"`
	Password string     `json:"password"`
	Address  netip.Addr `json:"address,omitempty"`
}
