package dns

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	boxDNS "github.com/sagernet/sing-box/dns"

	mDNS "github.com/miekg/dns"
)

func TestWriteStreamResponsePacksCompressedParseableMessage(t *testing.T) {
	response := &mDNS.Msg{
		MsgHdr:   mDNS.MsgHdr{Id: 1, Response: true},
		Question: []mDNS.Question{{Name: "repeated-name.example.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET}},
	}
	for index := range 8 {
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: response.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET},
			A:   net.IPv4(192, 0, 2, byte(index+1)),
		})
	}
	original := response.String()
	originalCompress := response.Compress
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		writeStreamResponse(serverConn, response)
		close(done)
	}()

	var responseLength uint16
	if err := binary.Read(clientConn, binary.BigEndian, &responseLength); err != nil {
		t.Fatal(err)
	}
	wireResponse := make([]byte, responseLength)
	if _, err := io.ReadFull(clientConn, wireResponse); err != nil {
		t.Fatal(err)
	}
	<-done

	if int(responseLength) != boxDNS.CompressedMessageLen(response) {
		t.Fatalf("response length = %d, want compressed length %d", responseLength, boxDNS.CompressedMessageLen(response))
	}
	if int(responseLength) >= boxDNS.UncompressedMessageLen(response) {
		t.Fatal("stream response was not compressed")
	}
	var unpacked mDNS.Msg
	if err := unpacked.Unpack(wireResponse); err != nil {
		t.Fatal(err)
	}
	if len(unpacked.Answer) != len(response.Answer) {
		t.Fatalf("answer count = %d, want %d", len(unpacked.Answer), len(response.Answer))
	}
	if response.String() != original || response.Compress != originalCompress {
		t.Fatal("writeStreamResponse mutated the caller-owned response")
	}
}
