package broker

import (
	"crypto/rand"
	"encoding/binary"
)

type Header struct {
	Magic byte // must be 0x56
	Type  payloadType
	ID    [16]byte
	Size  uint32
	// Parity is not stored here (it’s derived on encode/decode)
}

// parity over bytes[0:22] (magic,type,id,size), XOR of all
func headerParity(bytes22 []byte) (byte, error) {
	if len(bytes22) != HeaderLen-1 { // 22
		return 0, ErrInvalidHeaderParity
	}
	var x byte
	for _, b := range bytes22 {
		x ^= b
	}

	return x, nil
}

// DecodeHeader validates magic & parity and returns the parsed header.
func DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderLen {
		return Header{}, ErrIncompleteHeader
	}
	if src[0] != MagicByte {
		return Header{}, ErrInvalidHeaderBadMagic
	}
	// verify parity over [0..21]
	p, _ := headerParity(src[:22])
	if src[22] != p {
		return Header{}, ErrInvalidHeaderBadMagic
	}

	var h Header
	h.Magic = src[0]
	h.Type = payloadType(src[1])
	copy(h.ID[:], src[2:18])
	h.Size = binary.LittleEndian.Uint32(src[18:22])

	return h, nil
}

func NewMessageID() [16]byte {
	var id [16]byte
	_, _ = rand.Read(id[:])

	return id
}

func newMessage(msgType payloadType, id [16]byte, payload []byte) ([]byte, error) {
	payloadLen := len(payload)
	requiredSize := HeaderLen + payloadLen

	dst := make([]byte, requiredSize)
	if cap(dst) < requiredSize {
		return nil, ErrEncodeHeaderDestTooSmall
	}

	if payloadLen < 0 || payloadLen > int(^uint32(0)) {
		return nil, ErrEncodeHeaderPayloadLarge
	}

	// reslice to requiredSize
	dst[0] = MagicByte
	dst[1] = byte(msgType)
	copy(dst[2:18], id[:])
	binary.LittleEndian.PutUint32(dst[18:22], uint32(payloadLen))

	p, _ := headerParity(dst[:22])
	dst[22] = p
	copy(dst[HeaderLen:], payload)

	return dst, nil
}
