package dns

import (
	"github.com/sagernet/sing/common/buf"

	"github.com/miekg/dns"
)

func compressedMessage(message *dns.Msg) dns.Msg {
	compressed := *message
	compressed.Compress = true
	return compressed
}

// CompressedMessageLen returns the DNS wire length with name compression enabled.
func CompressedMessageLen(message *dns.Msg) int {
	compressed := compressedMessage(message)
	return compressed.Len()
}

// UncompressedMessageLen returns the DNS wire length with name compression disabled.
func UncompressedMessageLen(message *dns.Msg) int {
	uncompressed := *message
	uncompressed.Compress = false
	return uncompressed.Len()
}

// PackCompressedMessage packs message with DNS name compression enabled.
func PackCompressedMessage(message *dns.Msg, buffer []byte) ([]byte, error) {
	compressed := copyDNSMessage(message)
	compressed.Compress = true
	return compressed.PackBuffer(buffer)
}

func TruncateDNSMessage(request *dns.Msg, response *dns.Msg, frontHeadroom int, rearHeadroom int) (*buf.Buffer, error) {
	maxLen := 512
	if edns0Option := request.IsEdns0(); edns0Option != nil {
		if udpSize := int(edns0Option.UDPSize()); udpSize > 512 {
			maxLen = udpSize
		}
	}
	compressedResponseLen := CompressedMessageLen(response)
	packedResponse := copyDNSMessage(response)
	packedResponse.Compress = true
	if compressedResponseLen > maxLen {
		packedResponse.Truncate(maxLen)
		packedResponse.Compress = true
	}
	buffer := buf.NewSize(frontHeadroom + UncompressedMessageLen(packedResponse) + 1 + rearHeadroom)
	buffer.Resize(frontHeadroom, 0)
	rawMessage, err := packedResponse.PackBuffer(buffer.FreeBytes())
	if err != nil {
		buffer.Release()
		return nil, err
	}
	buffer.Truncate(len(rawMessage))
	return buffer, nil
}

func copyDNSMessage(message *dns.Msg) *dns.Msg {
	copied := *message
	copied.Question = append([]dns.Question(nil), message.Question...)
	copied.Answer = copyDNSRecords(message.Answer)
	copied.Ns = copyDNSRecords(message.Ns)
	copied.Extra = copyDNSRecords(message.Extra)
	return &copied
}

func copyDNSRecords(records []dns.RR) []dns.RR {
	if records == nil {
		return nil
	}
	copied := make([]dns.RR, len(records))
	for index, record := range records {
		if record != nil {
			copied[index] = dns.Copy(record)
		}
	}
	return copied
}
