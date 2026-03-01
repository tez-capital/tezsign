package common

import (
	"context"

	"github.com/google/gousb"
)

type libusbWriter struct {
	ep         *gousb.OutEndpoint
	packetSize int
}

func newLibusbWriter(ep *gousb.OutEndpoint) *libusbWriter {
	return &libusbWriter{ep: ep, packetSize: ep.Desc.MaxPacketSize}
}

func (w *libusbWriter) WriteContext(ctx context.Context, p []byte) (int, error) {
	total := len(p)
	written := 0
	for {
		chunk := p
		if len(chunk) > w.packetSize {
			chunk = chunk[:w.packetSize]
		}
		n, err := w.ep.WriteContext(ctx, chunk)
		if err != nil {
			return n, err
		}
		written += n
		if written == total {
			w.ep.WriteContext(ctx, []byte{}) // ZLP
			return n, nil
		}
		p = p[n:]
	}
}
