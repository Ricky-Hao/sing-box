package dns

import (
	"bytes"
	"net"
	"testing"

	mDNS "github.com/miekg/dns"
)

func TestTruncateDNSMessageCompressesBeforeTruncating(t *testing.T) {
	question := mDNS.Question{
		Name:   "very-long-iot-device-service-name.example.internal.",
		Qtype:  mDNS.TypeA,
		Qclass: mDNS.ClassINET,
	}
	request := &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{
			Id:               1,
			RecursionDesired: true,
		},
		Question: []mDNS.Question{question},
	}
	response := &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{
			Id:                 request.Id,
			Response:           true,
			Authoritative:      true,
			RecursionAvailable: true,
		},
		Question: []mDNS.Question{question},
	}
	for index := 0; response.Len() <= 512; index++ {
		if index >= 20 {
			t.Fatal("failed to construct a response that exceeds 512 bytes without compression")
		}
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{
				Name:   question.Name,
				Rrtype: mDNS.TypeA,
				Class:  mDNS.ClassINET,
				Ttl:    60,
			},
			A: net.IPv4(192, 0, 2, byte(index+1)),
		})
	}

	if response.Len() <= 512 {
		t.Fatal("test response must exceed the default UDP DNS limit without compression")
	}
	compressed := *response
	compressed.Compress = true
	if compressed.Len() > 512 {
		t.Fatal("test response must fit the default UDP DNS limit with compression")
	}

	buffer, err := TruncateDNSMessage(request, response, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Release()

	if buffer.Len() > 512 {
		t.Fatalf("compressed response length = %d, want <= 512", buffer.Len())
	}
	var unpacked mDNS.Msg
	if err = unpacked.Unpack(buffer.Bytes()); err != nil {
		t.Fatal(err)
	}
	if unpacked.Truncated {
		t.Fatal("response was truncated even though the compressed response fits")
	}
	if len(unpacked.Answer) != len(response.Answer) {
		t.Fatalf("answer count = %d, want %d", len(unpacked.Answer), len(response.Answer))
	}
	if response.Compress {
		t.Fatal("TruncateDNSMessage must not mutate the caller-owned response")
	}
}

func TestTruncateDNSMessageCompressedLimitBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		responseLen   int
		wantTruncated bool
	}{
		{"below limit", 511, false},
		{"equal to limit", 512, false},
		{"above limit", 513, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, response := responseWithCompressedLength(t, test.responseLen)
			original := response.String()
			originalCompress := response.Compress

			buffer, err := TruncateDNSMessage(request, response, 7, 11)
			if err != nil {
				t.Fatal(err)
			}
			defer buffer.Release()

			if buffer.Start() != 7 {
				t.Fatalf("front headroom = %d, want 7", buffer.Start())
			}
			if buffer.FreeLen() < 12 {
				t.Fatalf("remaining rear headroom = %d, want at least 12", buffer.FreeLen())
			}
			if buffer.Len() > 512 {
				t.Fatalf("response length = %d, want <= 512", buffer.Len())
			}
			var unpacked mDNS.Msg
			if err = unpacked.Unpack(buffer.Bytes()); err != nil {
				t.Fatal(err)
			}
			if unpacked.Truncated != test.wantTruncated {
				t.Fatalf("truncated = %v, want %v", unpacked.Truncated, test.wantTruncated)
			}
			if !test.wantTruncated && buffer.Len() != test.responseLen {
				t.Fatalf("response length = %d, want %d", buffer.Len(), test.responseLen)
			}
			repeatedBuffer, err := TruncateDNSMessage(request, response, 7, 11)
			if err != nil {
				t.Fatal(err)
			}
			defer repeatedBuffer.Release()
			if !bytes.Equal(repeatedBuffer.Bytes(), buffer.Bytes()) {
				t.Fatal("repeated call returned different wire response")
			}
			if response.String() != original || response.Compress != originalCompress {
				t.Fatal("TruncateDNSMessage mutated the caller-owned response")
			}
		})
	}
}

func TestTruncateDNSMessageDoesNotMutateEDNSRecord(t *testing.T) {
	request := newTestRequest()
	response := newTestResponse(request.Question[0])
	response.SetEdns0(4096, false)
	response.IsEdns0().SetExtendedRcode(16)
	original := response.String()
	originalExtendedRcode := response.IsEdns0().ExtendedRcode()

	buffer, err := TruncateDNSMessage(request, response, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	buffer.Release()

	if response.String() != original || response.IsEdns0().ExtendedRcode() != originalExtendedRcode {
		t.Fatal("TruncateDNSMessage mutated the caller-owned EDNS record")
	}
}

func TestTruncateDNSMessageReturnsPackErrorForMalformedResponse(t *testing.T) {
	request := newTestRequest()
	response := newTestResponse(request.Question[0])
	response.Answer = []mDNS.RR{nil}

	buffer, err := TruncateDNSMessage(request, response, 0, 0)

	if err == nil {
		if buffer != nil {
			buffer.Release()
		}
		t.Fatal("expected malformed response packing to fail")
	}
	if buffer != nil {
		buffer.Release()
		t.Fatal("buffer must be nil when packing fails")
	}
	if response.Compress || len(response.Answer) != 1 || response.Answer[0] != nil {
		t.Fatal("TruncateDNSMessage mutated the malformed caller-owned response")
	}
}

func responseWithCompressedLength(t *testing.T, target int) (*mDNS.Msg, *mDNS.Msg) {
	t.Helper()
	request := newTestRequest()
	response := newTestResponse(request.Question[0])
	for index := 0; index < 8; index++ {
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{
				Name:   request.Question[0].Name,
				Rrtype: mDNS.TypeA,
				Class:  mDNS.ClassINET,
				Ttl:    60,
			},
			A: net.IPv4(192, 0, 2, byte(index+1)),
		})
	}
	for payloadLen := 0; payloadLen <= 512; payloadLen++ {
		candidate := response.Copy()
		candidate.Answer = append(candidate.Answer, &mDNS.TXT{
			Hdr: mDNS.RR_Header{
				Name:   request.Question[0].Name,
				Rrtype: mDNS.TypeTXT,
				Class:  mDNS.ClassINET,
				Ttl:    60,
			},
			Txt: splitTXT(bytes.Repeat([]byte{'x'}, payloadLen)),
		})
		if CompressedMessageLen(candidate) == target {
			if UncompressedMessageLen(candidate) <= 512 {
				t.Fatal("test response must exceed 512 bytes without compression")
			}
			return request, candidate
		}
	}
	t.Fatalf("failed to construct response with compressed length %d", target)
	return nil, nil
}

func splitTXT(payload []byte) []string {
	var values []string
	for len(payload) > 255 {
		values = append(values, string(payload[:255]))
		payload = payload[255:]
	}
	return append(values, string(payload))
}

func newTestRequest() *mDNS.Msg {
	return &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []mDNS.Question{{
			Name:   "very-long-iot-device-service-name.example.internal.",
			Qtype:  mDNS.TypeA,
			Qclass: mDNS.ClassINET,
		}},
	}
}

func newTestResponse(question mDNS.Question) *mDNS.Msg {
	return &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{
			Id:                 1,
			Response:           true,
			Authoritative:      true,
			RecursionAvailable: true,
		},
		Question: []mDNS.Question{question},
	}
}
