package main

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestReaderContextCancellation(t *testing.T) {
	// Create a pipe for testing
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader, err := NewReader(r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}

	// Create a context that we'll cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Start reading in a goroutine
	done := make(chan struct{})
	var readErr error
	go func() {
		defer close(done)
		buf := make([]byte, 10)
		_, readErr = reader.ReadContext(ctx, buf)
	}()

	// Give it some time to start polling
	time.Sleep(150 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for the read to complete
	select {
	case <-done:
		// Expected - read should have returned
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ReadContext did not return after context cancellation")
	}

	// Verify we got context.Canceled error
	if readErr != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", readErr)
	}
}

func TestReaderSuccessfulRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader, err := NewReader(r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}

	// Write some data
	testData := []byte("hello")
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.Write(testData)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	buf := make([]byte, 10)
	n, err := reader.ReadContext(ctx, buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n != len(testData) {
		t.Errorf("expected %d bytes, got %d", len(testData), n)
	}

	if string(buf[:n]) != string(testData) {
		t.Errorf("expected %q, got %q", testData, buf[:n])
	}
}

func TestWriterContextCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	writer, err := NewWriter(w)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}

	// Create a context that we'll cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Try to write - should fail quickly
	_, writeErr := writer.WriteContext(ctx, []byte("test"))

	if writeErr != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", writeErr)
	}
}

func TestWriterSuccessfulWrite(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	writer, err := NewWriter(w)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}

	testData := []byte("hello world")

	// Read in background to prevent blocking
	var readBuf []byte
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 100)
		n, _ := r.Read(buf)
		readBuf = buf[:n]
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	n, err := writer.WriteContext(ctx, testData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if n != len(testData) {
		t.Errorf("expected %d bytes written, got %d", len(testData), n)
	}

	// Wait for read
	<-readDone

	if string(readBuf) != string(testData) {
		t.Errorf("expected %q, got %q", testData, readBuf)
	}
}

func TestReaderClose(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader, err := NewReader(r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}

	// Close the reader
	if err := reader.Close(); err != nil {
		t.Fatalf("failed to close reader: %v", err)
	}

	// Try to read - should fail with ErrClosed
	ctx := context.Background()
	_, readErr := reader.ReadContext(ctx, make([]byte, 10))

	if readErr != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got: %v", readErr)
	}
}

func TestWriterClose(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	writer, err := NewWriter(w)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}

	// Close the writer
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	// Try to write - should fail with ErrClosed
	ctx := context.Background()
	_, writeErr := writer.WriteContext(ctx, []byte("test"))

	if writeErr != os.ErrClosed {
		t.Errorf("expected os.ErrClosed, got: %v", writeErr)
	}
}

func TestReaderNoGoroutineLeak(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	reader, err := NewReader(r)
	if err != nil {
		t.Fatalf("failed to create reader: %v", err)
	}

	// Launch many read operations with canceled contexts
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			// Cancel after a short delay
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
			buf := make([]byte, 10)
			reader.ReadContext(ctx, buf)
		}()
	}

	// Wait for all to complete - they should all finish quickly
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines finished - no leak
	case <-time.After(2 * time.Second):
		t.Fatal("goroutines did not finish in time - possible leak")
	}
}

func TestWriterEmptyWrite(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	writer, err := NewWriter(w)
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}

	ctx := context.Background()
	n, err := writer.WriteContext(ctx, []byte{})

	if err != nil {
		t.Errorf("unexpected error for empty write: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written for empty slice, got %d", n)
	}
}
