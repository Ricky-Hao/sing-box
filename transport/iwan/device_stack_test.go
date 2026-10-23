//go:build with_gvisor

package iwan

import (
	"net/netip"
	"testing"

	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
	"github.com/sagernet/sing/common/logger"
)

func TestStackDeviceTunesTCP(t *testing.T) {
	t.Parallel()
	device, err := newStackDevice(EndpointOptions{
		Context: t.Context(),
		Logger:  logger.NOP(),
		MTU:     defaultMTU,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()

	var sendBuffer tcpip.TCPSendBufferSizeRangeOption
	if tcpErr := device.stack.TransportProtocolOption(tcp.ProtocolNumber, &sendBuffer); tcpErr != nil {
		t.Fatal(tcpErr)
	}
	if sendBuffer.Default != iwanTCPBufferDefault || sendBuffer.Max != iwanTCPBufferMax {
		t.Fatalf("unexpected TCP send buffer range: %+v", sendBuffer)
	}

	var receiveBuffer tcpip.TCPReceiveBufferSizeRangeOption
	if tcpErr := device.stack.TransportProtocolOption(tcp.ProtocolNumber, &receiveBuffer); tcpErr != nil {
		t.Fatal(tcpErr)
	}
	if receiveBuffer.Default != iwanTCPBufferDefault || receiveBuffer.Max != iwanTCPBufferMax {
		t.Fatalf("unexpected TCP receive buffer range: %+v", receiveBuffer)
	}

	var congestionControl tcpip.CongestionControlOption
	if tcpErr := device.stack.TransportProtocolOption(tcp.ProtocolNumber, &congestionControl); tcpErr != nil {
		t.Fatal(tcpErr)
	}
	if congestionControl != "cubic" {
		t.Fatalf("unexpected TCP congestion control: %s", congestionControl)
	}
}

func TestServerOutboundPayloadDestinationAcrossViews(t *testing.T) {
	t.Parallel()
	packet := buildIPv4Packet(netip.MustParseAddr("10.66.0.2"), netip.MustParseAddr("1.1.1.1"))
	payload := serverOutboundPayload{
		views: [][]byte{
			packet[:8],
			packet[8:17],
			packet[17:],
		},
		size: len(packet),
	}
	destination, ok := payload.destination()
	if !ok {
		t.Fatal("failed to parse destination")
	}
	if destination != netip.MustParseAddr("1.1.1.1") {
		t.Fatalf("unexpected destination: %v", destination)
	}
}
