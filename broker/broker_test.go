package broker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockReadWriter implements ReadContexter and WriteContexter for testing
type mockReadWriter struct {
	readData  chan []byte
	writeData chan []byte
	readErr   error
	writeErr  error
	mu        sync.Mutex
}

func newMockReadWriter() *mockReadWriter {
	return &mockReadWriter{
		readData:  make(chan []byte, 100),
		writeData: make(chan []byte, 100),
	}
}

func (m *mockReadWriter) ReadContext(ctx context.Context, p []byte) (int, error) {
	m.mu.Lock()
	err := m.readErr
	m.mu.Unlock()

	if err != nil {
		return 0, err
	}

	select {
	case data := <-m.readData:
		n := copy(p, data)
		return n, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (m *mockReadWriter) WriteContext(ctx context.Context, p []byte) (int, error) {
	m.mu.Lock()
	err := m.writeErr
	m.mu.Unlock()

	if err != nil {
		return 0, err
	}

	select {
	case m.writeData <- append([]byte(nil), p...):
		return len(p), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (m *mockReadWriter) setReadError(err error) {
	m.mu.Lock()
	m.readErr = err
	m.mu.Unlock()
}

func (m *mockReadWriter) setWriteError(err error) {
	m.mu.Lock()
	m.writeErr = err
	m.mu.Unlock()
}

func TestBrokerCreation(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	if b == nil {
		t.Fatal("broker should not be nil")
	}

	if b.workerCount != defaultWorkerCount {
		t.Errorf("expected default worker count %d, got %d", defaultWorkerCount, b.workerCount)
	}
}

func TestBrokerWithCustomWorkerCount(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	customCount := 16
	b := New(rw, rw, WithHandler(handler), WithWorkerCount(customCount))
	defer b.Stop()

	if b.workerCount != customCount {
		t.Errorf("expected worker count %d, got %d", customCount, b.workerCount)
	}
}

func TestBrokerPanicsWithoutHandler(t *testing.T) {
	rw := newMockReadWriter()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when creating broker without handler")
		}
	}()

	New(rw, rw) // Should panic
}

func TestBrokerStop(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler))

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("broker.Stop() did not complete in time")
	}
}

func TestWorkerPoolProcessesWork(t *testing.T) {
	rw := newMockReadWriter()

	var processedCount atomic.Int32
	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		processedCount.Add(1)
		return []byte("ok"), nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(4))
	defer b.Stop()

	// Simulate sending work items by directly using the work channel
	for i := 0; i < 10; i++ {
		b.workChan <- work{
			id:          [16]byte{byte(i)},
			payloadType: payloadTypeRequest,
			payload:     []byte("test"),
		}
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	if processedCount.Load() != 10 {
		t.Errorf("expected 10 items processed, got %d", processedCount.Load())
	}
}

func TestWorkerPoolConcurrency(t *testing.T) {
	rw := newMockReadWriter()

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		curr := currentConcurrent.Add(1)
		// Update max if current is higher
		for {
			max := maxConcurrent.Load()
			if curr <= max || maxConcurrent.CompareAndSwap(max, curr) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // Simulate work
		currentConcurrent.Add(-1)
		return nil, nil
	}

	workerCount := 4
	b := New(rw, rw, WithHandler(handler), WithWorkerCount(workerCount))
	defer b.Stop()

	// Send more work than workers
	for i := 0; i < 20; i++ {
		b.workChan <- work{
			id:          [16]byte{byte(i)},
			payloadType: payloadTypeRequest,
			payload:     []byte("test"),
		}
	}

	// Wait for all work to complete
	time.Sleep(500 * time.Millisecond)

	// Max concurrent should not exceed worker count
	if maxConcurrent.Load() > int32(workerCount) {
		t.Errorf("max concurrent %d exceeded worker count %d", maxConcurrent.Load(), workerCount)
	}
}

func TestWorkQueueFullDropsMessage(t *testing.T) {
	rw := newMockReadWriter()

	// Use a slow handler to fill up the queue
	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(1))
	defer b.Stop()

	// Fill up work queue
	for i := 0; i < workQueueSize+10; i++ {
		select {
		case b.workChan <- work{id: [16]byte{byte(i)}, payloadType: payloadTypeRequest}:
		default:
			// Queue full - this is expected for some items
		}
	}

	// The broker should handle this gracefully without blocking
}

func TestResponseChannelHandling(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("response"), nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	// Create a waiter
	id, ch := b.waiters.NewWaiter()

	// Send a response through the work channel
	b.workChan <- work{
		id:          id,
		payloadType: payloadTypeResponse,
		payload:     []byte("test response"),
	}

	// Should receive on the channel
	select {
	case resp := <-ch:
		if string(resp) != "test response" {
			t.Errorf("expected 'test response', got %q", resp)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive response in time")
	}
}

func TestBackoffConstants(t *testing.T) {
	// Verify backoff constants are reasonable
	if initialBackoff <= 0 {
		t.Error("initialBackoff should be positive")
	}
	if maxBackoff <= initialBackoff {
		t.Error("maxBackoff should be greater than initialBackoff")
	}
	if backoffFactor <= 1 {
		t.Error("backoffFactor should be greater than 1")
	}

	// Verify backoff progression
	backoff := initialBackoff
	iterations := 0
	for backoff < maxBackoff {
		backoff *= backoffFactor
		iterations++
		if iterations > 20 {
			t.Fatal("backoff did not reach maxBackoff in reasonable iterations")
		}
	}
}

func TestWriteFrameContextCancellation(t *testing.T) {
	rw := newMockReadWriter()

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler))
	defer b.Stop()

	// Fill the write channel
	for i := 0; i < 32; i++ {
		b.writeChan <- []byte("fill")
	}

	// Now try to write with canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.writeFrame(ctx, payloadTypeRequest, [16]byte{}, []byte("test"))
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestProcessingRequestsTracking(t *testing.T) {
	rw := newMockReadWriter()

	processing := make(chan struct{})
	done := make(chan struct{})

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		close(processing)
		<-done // Wait until we're told to finish
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(1))
	defer b.Stop()

	id := [16]byte{1, 2, 3}

	// Send a request
	b.workChan <- work{
		id:          id,
		payloadType: payloadTypeRequest,
		payload:     []byte("test"),
	}

	// Wait for processing to start
	<-processing

	// While processing, the request should be tracked
	if !b.processingRequests.HasRequest(id) {
		t.Error("request should be tracked while processing")
	}

	// Allow handler to finish
	close(done)
	time.Sleep(100 * time.Millisecond)

	// After processing, request should no longer be tracked
	if b.processingRequests.HasRequest(id) {
		t.Error("request should not be tracked after processing")
	}
}

func TestDuplicateRequestIgnored(t *testing.T) {
	rw := newMockReadWriter()

	var callCount atomic.Int32
	processing := make(chan struct{})

	handler := func(ctx context.Context, payload []byte) ([]byte, error) {
		callCount.Add(1)
		<-processing // Block until released
		return nil, nil
	}

	b := New(rw, rw, WithHandler(handler), WithWorkerCount(2))
	defer b.Stop()

	id := [16]byte{1, 2, 3}

	// Send the same request twice
	b.workChan <- work{id: id, payloadType: payloadTypeRequest, payload: []byte("test")}
	time.Sleep(50 * time.Millisecond) // Let first one start processing
	b.workChan <- work{id: id, payloadType: payloadTypeRequest, payload: []byte("test")}

	time.Sleep(100 * time.Millisecond)
	close(processing) // Release handlers
	time.Sleep(100 * time.Millisecond)

	// Only one should have been processed
	if callCount.Load() != 1 {
		t.Errorf("expected 1 handler call, got %d", callCount.Load())
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"EOF", errors.New("EOF"), false}, // Note: needs to be io.EOF
		{"random error", errors.New("random"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}
