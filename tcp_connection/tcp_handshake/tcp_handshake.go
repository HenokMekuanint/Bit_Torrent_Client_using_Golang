// handshake.go

package handshake

import (
	"fmt"
	"io"
)

// Handshake represents a handshake message between peers
type TCPHandshake struct {
	Protocol string
	InfoHash [20]byte
	PeerID   [20]byte
}

// Serialize serializes a handshake message into bytes
func (h *TCPHandshake) Serialize() []byte {
	buf := make([]byte, len(h.Protocol)+49)
	buf[0] = byte(len(h.Protocol))
	curr := 1
	curr += copy(buf[curr:], h.Protocol)
	curr += copy(buf[curr:], make([]byte, 8)) // 8 reserved bytes
	curr += copy(buf[curr:], h.InfoHash[:])
	curr += copy(buf[curr:], h.PeerID[:])
	return buf
}

// Read reads a handshake message from an io.Reader
func ReadHandshake(r io.Reader) (*TCPHandshake, error) {
	// Read the protocol length
	protocolLenBuf := make([]byte, 1)
	_, err := io.ReadFull(r, protocolLenBuf)
	if err != nil {
		return nil, err
	}
	protocolLen := int(protocolLenBuf[0])

	if protocolLen == 0 {
		return nil, fmt.Errorf("protocol length cannot be 0")
	}

	// Read the rest of the handshake message
	handshakeBuf := make([]byte, 48+protocolLen)
	_, err = io.ReadFull(r, handshakeBuf)
	if err != nil {
		return nil, err
	}

	// Parse the handshake message
	var infoHash, peerID [20]byte
	copy(infoHash[:], handshakeBuf[protocolLen+8:protocolLen+8+20])
	copy(peerID[:], handshakeBuf[protocolLen+8+20:])

	h := TCPHandshake{
		Protocol: string(handshakeBuf[0:protocolLen]),
		InfoHash: infoHash,
		PeerID:   peerID,
	}

	return &h, nil
}


