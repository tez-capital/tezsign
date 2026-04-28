package broker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"syscall"
	"testing"
	"time"
)

type blockingReader struct{}

func (blockingReader) ReadContext(ctx context.Context, _ []byte) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

type scriptedWriter struct {
	mu    sync.Mutex
	errOn []error
	calls int
}

func (w *scriptedWriter) WriteContext(_ context.Context, p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	call := w.calls
	w.calls++
	if call < len(w.errOn) && w.errOn[call] != nil {
		return 0, w.errOn[call]
	}
	return len(p), nil
}

func (w *scriptedWriter) Calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func newTestBroker(t *testing.T, writer WriteContexter) *Broker {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(blockingReader{}, writer,
		WithLogger(logger),
		WithHandler(func(context.Context, []byte) ([]byte, error) {
			return nil, nil
		}),
	)
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestWriterLoopRetriesOnceOnRetryableError(t *testing.T) {
	writer := &scriptedWriter{errOn: []error{syscall.EAGAIN, nil}}
	b := newTestBroker(t, writer)
	defer b.Stop()

	if err := b.writeFrame(context.Background(), payloadTypeRequest, [16]byte{1}, []byte("payload")); err != nil {
		t.Fatalf("writeFrame failed: %v", err)
	}

	waitForCondition(t, func() bool { return writer.Calls() >= 2 })
	if got := writer.Calls(); got != 2 {
		t.Fatalf("expected 2 write attempts, got %d", got)
	}
	if err := b.ctx.Err(); err != nil {
		t.Fatalf("broker canceled unexpectedly: %v", err)
	}
}

func TestWriterLoopDropsFrameAfterRetryLimit(t *testing.T) {
	writer := &scriptedWriter{
		errOn: []error{
			syscall.EAGAIN,
			syscall.EAGAIN,
			nil,
		},
	}
	b := newTestBroker(t, writer)
	defer b.Stop()

	if err := b.writeFrame(context.Background(), payloadTypeRequest, [16]byte{2}, []byte("payload")); err != nil {
		t.Fatalf("writeFrame failed: %v", err)
	}

	waitForCondition(t, func() bool { return writer.Calls() >= 2 })

	if got := writer.Calls(); got != 2 {
		t.Fatalf("expected 2 write attempts before dropping frame, got %d", got)
	}

	if err := b.ctx.Err(); err != nil {
		t.Fatalf("broker canceled unexpectedly after dropped frame: %v", err)
	}

	if err := b.writeFrame(context.Background(), payloadTypeRequest, [16]byte{3}, []byte("next")); err != nil {
		t.Fatalf("writeFrame for next frame failed: %v", err)
	}

	waitForCondition(t, func() bool { return writer.Calls() >= 3 })

	if got := writer.Calls(); got != 3 {
		t.Fatalf("expected next frame to be written after drop, got %d calls", got)
	}

	if err := b.ctx.Err(); err != nil {
		t.Fatalf("broker canceled unexpectedly after next frame: %v", err)
	}
}
