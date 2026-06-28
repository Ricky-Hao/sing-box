//go:build with_gvisor && linux

package iwan

import (
	"net/netip"
	"testing"
)

func TestSystemTunAddressUsesFirstHost(t *testing.T) {
	t.Parallel()
	address := systemTunAddress(netip.MustParsePrefix("10.66.0.0/24"))
	if address != netip.MustParsePrefix("10.66.0.1/24") {
		t.Fatalf("unexpected system tun address: %v", address)
	}
}
