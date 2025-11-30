package broker

import "errors"

var (
	ErrInvalidHeaderParity   = errors.New("invalid header parity")
	ErrIncompleteHeader      = errors.New("incomplete header")
	ErrInvalidHeaderBadMagic = errors.New("invalid header magic")

	ErrNoPayloadFound     = errors.New("no payload found")
	ErrIncompletePayload  = errors.New("incomplete payload")
	ErrInvalidPayload     = errors.New("invalid payload")
	ErrInvalidPayloadSize = errors.New("invalid payload size")

	ErrEncodeHeaderDestTooSmall      = errors.New("header encode: dst too small")
	ErrEncodeHeaderPayloadLarge      = errors.New("header encode: payload too large")
	ErrEncodeFrameInvalidPayloadSize = errors.New("frame encode: invalid payload size")
	ErrDecodeHeaderShort             = errors.New("short header")
	ErrDecodeHeaderBadMagic          = errors.New("bad magic")
	ErrDecodeHeaderBadParity         = errors.New("bad parity")
)
