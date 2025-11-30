package broker

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
)

type stash struct {
	buf      bytes.Buffer
	capacity int
	logger   *slog.Logger
}

func newStash(size int, logger *slog.Logger) *stash {
	return &stash{
		capacity: size,
		logger:   logger,
	}
}

func (s *stash) Len() int {
	return s.buf.Len()
}

func (s *stash) Write(data []byte) (int, error) {
	dataLen := len(data)

	if s.buf.Len()+dataLen > s.capacity {
		drop := (s.buf.Len() + dataLen) - s.capacity
		s.buf.Next(drop)
		s.logger.Warn("stash overflow: dropped oldest", slog.Int("drop", drop), slog.Int("stash_len", s.buf.Len()), slog.Int("capacity", s.capacity))
	}

	return s.buf.Write(data)
}

func (s *stash) ReadPayload() ([16]byte, payloadType, []byte, error) {
	var id [16]byte
	data := s.buf.Bytes()

	idx := bytes.IndexByte(data, MagicByte)
	if idx < 0 {
		// no magic at all: drop everything except a small tail to avoid growth
		if drop := s.buf.Len() - (HeaderLen - 1); drop > 0 {
			s.buf.Next(drop)
		}
		return id, payloadTypeUnknown, nil, ErrNoPayloadFound
	}

	s.buf.Next(idx) // drop bytes in front of magic
	data = s.buf.Bytes()

	h, err := DecodeHeader(data)
	if err != nil {
		s.logger.Debug("bad header decode; resync")
		if err != ErrIncompleteHeader {
			// skip magic byte only if header is definitely bad
			s.buf.Next(1)
		}
		return id, payloadTypeUnknown, nil, errors.Join(ErrInvalidPayload, err)
	}

	if int(h.Size) > MAX_MESSAGE_PAYLOAD {
		s.logger.Warn("drop oversized frame", slog.String("type", fmt.Sprintf("%02x", h.Type)), slog.String("id", fmt.Sprintf("%x", h.ID)), slog.Int("size", int(h.Size)), slog.Int("limit", MAX_MESSAGE_PAYLOAD))
		// NOTE:
		// it is not good idea to drop HeaderLen+Size bytes here,
		// because someone could send a lot of garbage with valid headers
		// and we would end up dropping a lot of valid messages.
		// Instead, we just drop the header and try to resync.
		s.buf.Next(HeaderLen)
		return id, payloadTypeUnknown, nil, ErrInvalidPayloadSize
	}

	total := int(HeaderLen + h.Size)
	if len(data) < total {
		// wait for full payload
		return id, payloadTypeUnknown, nil, ErrIncompletePayload
	}

	s.logger.Debug("rx hdr", slog.String("type", fmt.Sprintf("%02x", h.Type)), slog.String("id", fmt.Sprintf("%x", h.ID)), slog.Int("size", int(h.Size)))
	s.buf.Next(HeaderLen) // consume header

	// we do not need io.ReadFull here, because we already verified that we have full payload
	payloadBuffer := s.buf.Next(int(h.Size))
	// there may be sensitive data in payloadBuffer, so clear it after use
	// we copy it to result slice, which we return to caller
	// who is responsible for clearing it after use
	defer clear(payloadBuffer)

	// we don't use bytes.Clone, because it can create a larger than needed slice
	// and we know exact size here
	result := make([]byte, h.Size)
	copy(result, payloadBuffer)

	return h.ID, h.Type, result, nil
}
