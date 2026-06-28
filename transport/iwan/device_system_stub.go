//go:build with_gvisor && !linux

package iwan

import E "github.com/sagernet/sing/common/exceptions"

func newSystemTunDevice(options ServerOptions) (serverDevice, error) {
	return nil, E.New("iWAN system server mode is only supported on Linux")
}
