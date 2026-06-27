//go:build with_gvisor && linux

package iwan

import (
	"io"
	"net/netip"
	"os"
	"sync"

	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
)

type systemTunDevice struct {
	options    ServerOptions
	tunOptions tun.Options
	tunIf      tun.Tun
	closeOnce  sync.Once
}

func newSystemTunDevice(options ServerOptions) (serverDevice, error) {
	if options.InterfaceName == "" {
		options.InterfaceName = tun.CalculateInterfaceName("iwan")
	}
	device := &systemTunDevice{
		options: options,
		tunOptions: tun.Options{
			Name:             options.InterfaceName,
			Inet4Address:     []netip.Prefix{systemTunAddress(options.AddressPool)},
			MTU:              options.MTU,
			InterfaceMonitor: options.InterfaceMonitor,
			InterfaceFinder:  options.InterfaceFinder,
			Logger:           options.Logger,
		},
	}
	return device, nil
}

func systemTunAddress(addressPool netip.Prefix) netip.Prefix {
	return netip.PrefixFrom(uint32ToIPv4(ipv4ToUint32(addressPool.Masked().Addr())+1), addressPool.Bits())
}

func (d *systemTunDevice) Start() error {
	if d.tunIf != nil {
		return nil
	}
	if d.tunOptions.InterfaceMonitor == nil {
		return E.New("missing interface monitor for iWAN system server")
	}
	tunIf, err := tun.New(d.tunOptions)
	if err != nil {
		return E.Cause(err, "configure iWAN system tun interface")
	}
	err = tunIf.Start()
	if err != nil {
		_ = tunIf.Close()
		return E.Cause(err, "start iWAN system tun interface")
	}
	d.tunIf = tunIf
	d.options.Logger.Info("started iWAN system interface at ", d.tunOptions.Name)
	return nil
}

func (d *systemTunDevice) Read(packet []byte) (int, error) {
	if d.tunIf == nil {
		return 0, os.ErrClosed
	}
	return d.tunIf.Read(packet)
}

func (d *systemTunDevice) Write(packet []byte) error {
	if len(packet) == 0 {
		return nil
	}
	if d.tunIf == nil {
		return os.ErrClosed
	}
	n, err := d.tunIf.Write(packet)
	if err == nil && n != len(packet) {
		return io.ErrShortWrite
	}
	return err
}

func (d *systemTunDevice) Close() error {
	var err error
	d.closeOnce.Do(func() {
		if d.tunIf != nil {
			err = d.tunIf.Close()
		}
	})
	return err
}
