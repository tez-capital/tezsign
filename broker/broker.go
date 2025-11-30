package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"syscall"

	"github.com/tez-capital/tezsign/logging"
)

// Optional capabilities (used via type assertions).
type ReadContexter interface {
	ReadContext(ctx context.Context, p []byte) (int, error)
}

type WriteContexter interface {
	WriteContext(ctx context.Context, p []byte) (int, error)
}

type Handler func(ctx context.Context, payload []byte) ([]byte, error)

type options struct {
	bufSize int
	handler Handler
	logger  *slog.Logger
}

type Option func(*options)

func WithBufferSize(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.bufSize = n
		}
	}
}

func WithHandler(h Handler) Option {
	return func(o *options) { o.handler = h }
}

func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

type Broker struct {
	r ReadContexter
	w WriteContexter

	stash *stash

	waiters waiterMap
	handler Handler

	writeChan           chan []byte
	processingRequests  requestMap[struct{}]
	unconfirmedRequests requestMap[[]byte]

	capacity int
	logger   *slog.Logger

	ctx            context.Context
	cancel         context.CancelFunc
	readLoopDone   <-chan struct{}
	writerLoopDone <-chan struct{}
}

func New(r ReadContexter, w WriteContexter, opts ...Option) *Broker {
	o := &options{
		bufSize: DEFAULT_BROKER_CAPACITY,
	}
	for _, fn := range opts {
		fn(o)
	}

	if o.logger == nil {
		o.logger, _ = logging.NewFromEnv()
	}

	if o.handler == nil {
		panic("broker: handler is required (use WithHandler)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{
		r:        r,
		w:        w,
		capacity: o.bufSize,
		logger:   o.logger,
		handler:  o.handler,

		writeChan:           make(chan []byte, 32),
		processingRequests:  NewRequestMap[struct{}](),
		unconfirmedRequests: NewRequestMap[[]byte](),

		stash:  newStash(o.bufSize, o.logger),
		ctx:    ctx,
		cancel: cancel,
	}

	b.readLoopDone = b.readLoop()
	b.writerLoopDone = b.writerLoop()
	return b
}

func (b *Broker) Request(ctx context.Context, payload []byte) ([]byte, [16]byte, error) {
	var id [16]byte
	payloadLen := len(payload)
	if payloadLen > int(^uint32(0)) {
		return nil, id, fmt.Errorf("payload too large")
	}

	if payloadLen > MAX_MESSAGE_PAYLOAD {
		return nil, id, fmt.Errorf("payload exceeds maximum message payload (%d bytes)", MAX_MESSAGE_PAYLOAD)
	}

	id, ch := b.waiters.NewWaiter()
	b.unconfirmedRequests.Store(id, payload)

	b.logger.Debug("tx req", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", payloadLen))

	if err := b.writeFrame(ctx, payloadTypeRequest, id, payload); err != nil {
		b.logger.Debug("tx req write failed", slog.String("id", fmt.Sprintf("%x", id)), slog.Any("err", err))
		b.waiters.Delete(id)
		return nil, id, err
	}

	select {
	case resp := <-ch:
		return resp, id, nil
	case <-ctx.Done():
		b.unconfirmedRequests.Delete(id)
		b.waiters.Delete(id)
		return nil, id, ctx.Err()
	case <-b.ctx.Done():
		b.unconfirmedRequests.Delete(id)
		b.waiters.Delete(id)
		return nil, id, io.EOF
	}
}

func (b *Broker) writerLoop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var data []byte
			select {
			case data = <-b.writeChan:
			case <-b.ctx.Done():
				return
			}
			if _, err := b.w.WriteContext(b.ctx, data); err != nil {
				if isRetryable(err) {
					b.logger.Debug("write retryable error", slog.Any("err", err))
					continue
				}
				b.logger.Debug("write loop exit", slog.Any("err", err))
				return
			}
		}
	}()
	return done
}

func (b *Broker) readLoop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [DEFAULT_READ_BUFFER]byte
		for {
			n, err := b.r.ReadContext(b.ctx, buf[:])
			if n > 0 {
				b.stash.Write(buf[:n])
				clear(buf[:n]) // clear buffer after we used it
				b.processStash()
			}

			if err != nil {
				if isRetryable(err) {
					// send retry packet to
					b.writeFrame(b.ctx, payloadTypeRetry, [16]byte{}, nil)
					b.logger.Debug("read retryable error", slog.Any("err", err))
					continue
				}
				b.logger.Debug("read loop exit", slog.Any("err", err))
				return
			}
		}
	}()
	return done
}

func (b *Broker) processStash() {
	for {
		id, pt, payload, err := b.stash.ReadPayload()
		switch {
		case errors.Is(err, ErrNoPayloadFound):
			fallthrough
		case errors.Is(err, ErrIncompletePayload):
			runtime.GC() // encourage freeing stash buffers
			return
		case errors.Is(err, ErrInvalidPayloadSize):
			continue // resync
		case err != nil:
			b.logger.Warn("bad payload; resync", slog.Any("err", err))
			continue // resync
		}

		go func(id [16]byte, payloadType payloadType, payload []byte) {
			switch payloadType {
			case payloadTypeResponse:
				b.logger.Debug("rx resp", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(payload)))
				if ch, ok := b.waiters.LoadAndDelete(id); ok && ch != nil {
					ch <- payload
				}
			case payloadTypeRequest:
				b.logger.Debug("rx req", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(payload)))
				if processing := b.processingRequests.HasRequest(id); processing {
					b.logger.Debug("duplicate request being processed; ignoring", slog.String("id", fmt.Sprintf("%x", id)))
					return
				}
				b.processingRequests.Store(id, struct{}{})

				// accept the request immediately
				b.writeFrame(b.ctx, payloadTypeAcceptRequest, id, nil)

				if b.handler == nil {
					return
				}
				defer b.processingRequests.Delete(id)
				resp, _ := b.handler(b.ctx, payload)

				b.logger.Debug("tx resp", slog.String("id", fmt.Sprintf("%x", id)), slog.Int("size", len(resp)))
				_ = b.writeFrame(b.ctx, payloadTypeResponse, id, resp) // Put is deferred inside writeFrame if pooled
			case payloadTypeAcceptRequest:
				b.logger.Debug("rx accept", slog.String("id", fmt.Sprintf("%x", id)))
				b.unconfirmedRequests.Delete(id)
			case payloadTypeRetry:
				b.logger.Debug("rx retry", slog.String("id", fmt.Sprintf("%x", id)))
				allUnconfirmed := b.unconfirmedRequests.All()
				for reqID, reqPayload := range allUnconfirmed {
					b.writeFrame(b.ctx, payloadTypeRequest, reqID, reqPayload)
				}
			default:
				b.logger.Warn("unknown type; resync", slog.String("type", fmt.Sprintf("%02x", payloadType)), slog.String("id", fmt.Sprintf("%x", id)))
			}
		}(id, pt, payload)
	}
}

// writeFrame writes header+payload in one go.
// Uses pooled buffer for payloads <= MAX_POOLED_PAYLOAD and defers Put.
// Larger payloads allocate a right-sized frame (no Put).
func (b *Broker) writeFrame(ctx context.Context, msgType payloadType, id [16]byte, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("panic in Request", slog.Any("recover", r))
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	frame, err := newMessage(msgType, id, payload)
	if err != nil {
		return err
	}

	b.writeChan <- frame
	return nil
}

func (b *Broker) Stop() {
	b.cancel()
	<-b.readLoopDone
	<-b.writerLoopDone
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// USB endpoints can bounce during (re)bind, host opens, etc.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EAGAIN,
			syscall.EINTR,
			syscall.EIO,
			syscall.ENODEV,
			syscall.EPROTO,
			syscall.ESHUTDOWN,
			syscall.EBADMSG,
			syscall.ETIMEDOUT:
			return true
		}
	}
	return false
}
