package main

import "testing"

func TestSharedTagsRetainRequiredMobileFeatures(t *testing.T) {
	// Given
	tagCounts := make(map[string]int, len(sharedTags))
	for _, tag := range sharedTags {
		tagCounts[tag]++
	}

	// When
	requiredTagCounts := map[string]int{
		"with_iwan":   1,
		"with_gvisor": 1,
	}

	// Then
	for tag, wantCount := range requiredTagCounts {
		if gotCount := tagCounts[tag]; gotCount != wantCount {
			t.Errorf("shared tag %q count = %d, want %d", tag, gotCount, wantCount)
		}
	}
	for _, tag := range []string{
		"with_quic",
		"with_wireguard",
		"with_utls",
		"with_naive_outbound",
		"with_clash_api",
		"with_usbip",
		"with_openvpn",
		"with_openconnect",
	} {
		if tagCounts[tag] == 0 {
			t.Errorf("shared tags do not retain upstream feature %q", tag)
		}
	}
}
