package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"syscall"
	"time"

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
	bufSize     int
	handler     Handler
	logger      *slog.Logger
	workerCount int
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

// WithWorkerCount sets the number of worker goroutines for handling requests.
// Default is 8.
func WithWorkerCount(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.workerCount = n
		}
	}
}

// work represents a unit of work for the worker pool
type work struct {
	id          [16]byte
	payloadType payloadType
	payload     []byte
}

type Broker struct {
	r ReadContexter
	w WriteContexter

	stash *stash

	waiters waiterMap
	handler Handler

	writeChan           chan []byte
	workChan            chan work // bounded channel for worker pool
	processingRequests  requestMap[struct{}]
	unconfirmedRequests requestMap[[]byte]

	capacity    int
	workerCount int
	logger      *slog.Logger

	ctx            context.Context
	cancel         context.CancelFunc
	readLoopDone   <-chan struct{}
	writerLoopDone <-chan struct{}
	workersDone    <-chan struct{}
}

const (
	defaultWorkerCount = 8
	workQueueSize      = 64

	// Backoff constants for retry loops
	initialBackoff = 10 * time.Millisecond
	maxBackoff     = 1 * time.Second
	backoffFactor  = 2
)

func New(r ReadContexter, w WriteContexter, opts ...Option) *Broker {
	o := &options{
		bufSize:     DEFAULT_BROKER_CAPACITY,
		workerCount: defaultWorkerCount,
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
		r:           r,
		w:           w,
		capacity:    o.bufSize,
		workerCount: o.workerCount,
		logger:      o.logger,
		handler:     o.handler,

		writeChan:           make(chan []byte, 32),
		workChan:            make(chan work, workQueueSize),
		processingRequests:  NewRequestMap[struct{}](),
		unconfirmedRequests: NewRequestMap[[]byte](),

		stash:  newStash(o.bufSize, o.logger),
		ctx:    ctx,
		cancel: cancel,
	}

	b.workersDone = b.startWorkers()
	b.readLoopDone = b.readLoop()
	b.writerLoopDone = b.writerLoop()
	return b
}

// startWorkers launches a fixed pool of worker goroutines
func (b *Broker) startWorkers() <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		workerDone := make(chan struct{}, b.workerCount)

		for i := 0; i < b.workerCount; i++ {
			go func() {
				defer func() { workerDone <- struct{}{} }()
				for {
					select {
					case w, ok := <-b.workChan:
						if !ok {
							return
						}
						b.handleWork(w)
					case <-b.ctx.Done():
						return
					}
				}
			}()
		}

		// Wait for all workers to finish
		for i := 0; i < b.workerCount; i++ {
			<-workerDone
		}
	}()

	return done
}

// handleWork processes a single work item
func (b *Broker) handleWork(w work) {
	switch w.payloadType {
	case payloadTypeResponse:
		b.logger.Debug("rx resp", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(w.payload)))
		if ch, ok := b.waiters.LoadAndDelete(w.id); ok && ch != nil {
			select {
			case ch <- w.payload:
			default:
				// Channel full or closed, drop response
				b.logger.Warn("response channel full, dropping", slog.String("id", fmt.Sprintf("%x", w.id)))
			}
		}
	case payloadTypeRequest:
		b.logger.Debug("rx req", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(w.payload)))
		if processing := b.processingRequests.HasRequest(w.id); processing {
			b.logger.Debug("duplicate request being processed; ignoring", slog.String("id", fmt.Sprintf("%x", w.id)))
			return
		}
		b.processingRequests.Store(w.id, struct{}{})

		// accept the request immediately
		b.writeFrame(b.ctx, payloadTypeAcceptRequest, w.id, nil)

		if b.handler == nil {
			b.processingRequests.Delete(w.id)
			return
		}
		resp, _ := b.handler(b.ctx, w.payload)
		b.processingRequests.Delete(w.id)

		b.logger.Debug("tx resp", slog.String("id", fmt.Sprintf("%x", w.id)), slog.Int("size", len(resp)))
		_ = b.writeFrame(b.ctx, payloadTypeResponse, w.id, resp)
	case payloadTypeAcceptRequest:
		b.logger.Debug("rx accept", slog.String("id", fmt.Sprintf("%x", w.id)))
		b.unconfirmedRequests.Delete(w.id)
	case payloadTypeRetry:
		b.logger.Debug("rx retry", slog.String("id", fmt.Sprintf("%x", w.id)))
		allUnconfirmed := b.unconfirmedRequests.All()
		for reqID, reqPayload := range allUnconfirmed {
			b.writeFrame(b.ctx, payloadTypeRequest, reqID, reqPayload)
		}
	default:
		b.logger.Warn("unknown type; resync", slog.String("type", fmt.Sprintf("%02x", w.payloadType)), slog.String("id", fmt.Sprintf("%x", w.id)))
	}
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
		backoff := initialBackoff

		for {
			var data []byte
			select {
			case data = <-b.writeChan:
			case <-b.ctx.Done():
				return
			}

			for {
				select {
				case <-b.ctx.Done():
					return
				default:
				}

				if _, err := b.w.WriteContext(b.ctx, data); err != nil {
					if isRetryable(err) {
						b.logger.Debug("write retryable error, backing off", slog.Any("err", err), slog.Duration("backoff", backoff))

						// Exponential backoff with context check
						select {
						case <-time.After(backoff):
						case <-b.ctx.Done():
							return
						}

						backoff *= backoffFactor
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
						continue
					}
					b.logger.Error("write loop exit", slog.Any("err", err))
					return
				}
				// Success - reset backoff
				backoff = initialBackoff
				break
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
		backoff := initialBackoff

		for {
			select {
			case <-b.ctx.Done():
				return
			default:
			}

			n, err := b.r.ReadContext(b.ctx, buf[:])
			if n > 0 {
				b.stash.Write(buf[:n])
				clear(buf[:n]) // clear buffer after we used it
				b.processStash()
				// Reset backoff on successful read
				backoff = initialBackoff
			}

			if err != nil {
				if isRetryable(err) {
					// send retry packet
					b.writeFrame(b.ctx, payloadTypeRetry, [16]byte{}, nil)
					b.logger.Debug("read retryable error, backing off", slog.Any("err", err), slog.Duration("backoff", backoff))

					// Exponential backoff with context check
					select {
					case <-time.After(backoff):
					case <-b.ctx.Done():
						return
					}

					backoff *= backoffFactor
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
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
			return
		case errors.Is(err, ErrIncompletePayload):
			// Removed runtime.GC() - let Go manage GC naturally
			return
		case errors.Is(err, ErrInvalidPayloadSize):
			continue // resync
		case err != nil:
			b.logger.Warn("bad payload; resync", slog.Any("err", err))
			continue // resync
		}

		// Send work to the worker pool (bounded queue)
		w := work{id: id, payloadType: pt, payload: payload}
		select {
		case b.workChan <- w:
			// Successfully queued
		case <-b.ctx.Done():
			return
		default:
			// Work queue full - log warning but don't block
			b.logger.Warn("work queue full, dropping message",
				slog.String("type", fmt.Sprintf("%02x", pt)),
				slog.String("id", fmt.Sprintf("%x", id)))
		}
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
		b.logger.Error("failed to create message frame", slog.Any("error", err))
		return err
	}

	// Non-blocking send with context awareness
	select {
	case b.writeChan <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return io.EOF
	}
}

func (b *Broker) Stop() {
	b.cancel()
	<-b.readLoopDone
	<-b.writerLoopDone
	close(b.workChan)
	<-b.workersDone
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
