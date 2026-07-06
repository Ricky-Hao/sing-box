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
	compressed := compressedMessage(message)
	return compressed.PackBuffer(buffer)
}

func TruncateDNSMessage(request *dns.Msg, response *dns.Msg, headroom int) (*buf.Buffer, error) {
	maxLen := 512
	if edns0Option := request.IsEdns0(); edns0Option != nil {
		if udpSize := int(edns0Option.UDPSize()); udpSize > 512 {
			maxLen = udpSize
		}
	}
	compressedResponseLen := CompressedMessageLen(response)
	if compressedResponseLen > maxLen {
		response = response.Copy()
		response.Compress = true
		response.Truncate(maxLen)
	}
	buffer := buf.NewSize(headroom*2 + 1 + UncompressedMessageLen(response))
	buffer.Resize(headroom, 0)
	rawMessage, err := PackCompressedMessage(response, buffer.FreeBytes())
	if err != nil {
		buffer.Release()
		return nil, err
	}
	buffer.Truncate(len(rawMessage))
	return buffer, nil
}
