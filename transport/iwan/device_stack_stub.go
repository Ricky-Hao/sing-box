//go:build !with_gvisor

package iwan

import "github.com/sagernet/sing-tun"

type stackDevice struct{}

func newStackDevice(options EndpointOptions) (*stackDevice, error) {
	return nil, tun.ErrGVisorNotIncluded
}
