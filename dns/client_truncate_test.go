package dns

import (
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

	buffer, err := TruncateDNSMessage(request, response, 0)
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
