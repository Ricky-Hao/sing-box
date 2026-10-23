package iwan

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildOpenPacketRoundTripsMaximumUsername(t *testing.T) {
	t.Parallel()
	username := strings.Repeat("a", 253)

	packet, err := buildOpenPacket(username, "mypassword", defaultMTU, false, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	usernameTLV := packet[signedHeader+4:]
	if usernameTLV[0] != 1 || usernameTLV[1] != 255 {
		t.Fatalf("unexpected username TLV header: %x", usernameTLV[:2])
	}
	info, err := parseOpenPacket(packet)
	if err != nil {
		t.Fatal(err)
	}
	if info.username != username {
		t.Fatal("maximum username did not round trip")
	}
}

func TestBuildOpenPacketUsesUsernameByteLength(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		username   string
		byteLength int
		valid      bool
	}{
		{name: "maximum multibyte username", username: strings.Repeat("界", 84) + "a", byteLength: 253, valid: true},
		{name: "254-byte ASCII username", username: strings.Repeat("a", 254), byteLength: 254},
		{name: "254-byte multibyte username", username: strings.Repeat("界", 84) + "ab", byteLength: 254},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(test.username); got != test.byteLength {
				t.Fatalf("invalid test fixture byte length: %d", got)
			}
			packet, err := buildOpenPacket(test.username, "mypassword", defaultMTU, false, 0, 0)
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
				info, parseErr := parseOpenPacket(packet)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				if info.username != test.username {
					t.Fatal("multibyte username did not round trip")
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted username that overflows OPEN TLV: %x", packet[signedHeader+4:signedHeader+6])
			}
			if !errors.Is(err, errUsernameTooLong) {
				t.Fatalf("unexpected username error: %v", err)
			}
			if packet != nil {
				t.Fatal("returned malformed OPEN packet with username error")
			}
		})
	}
}
