package main

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

type result struct {
	n   int
	err error
}

type Reader struct {
	fd     int
	file   *os.File
	mu     sync.Mutex
	closed bool
}

type Writer struct {
	fd     int
	file   *os.File
	mu     sync.Mutex
	closed bool
}

func NewReader(f *os.File) (*Reader, error) {
	return &Reader{fd: int(f.Fd()), file: f}, nil
}

func NewWriter(f *os.File) (*Writer, error) {
	return &Writer{fd: int(f.Fd()), file: f}, nil
}

// closeOnce closes the underlying file descriptor once.
// This unblocks any goroutine stuck in unix.Read.
func (r *Reader) closeOnce() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		_ = r.file.Close()
	}
}

func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	readChan := make(chan result, 1)

	go func() {
		n, err := unix.Read(r.fd, p)
		readChan <- result{n: n, err: err}
	}()

	select {
	case <-ctx.Done():
		// Close the file to unblock the goroutine stuck in unix.Read
		r.closeOnce()
		// Wait for the goroutine to actually exit (prevents leak)
		<-readChan
		return 0, ctx.Err()
	case res := <-readChan:
		if errors.Is(res.err, os.ErrDeadlineExceeded) {
			return 0, ctx.Err()
		}
		return res.n, res.err
	}
}

// closeOnce closes the underlying file descriptor once.
// This unblocks any goroutine stuck in unix.Write.
func (w *Writer) closeOnce() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		_ = w.file.Close()
	}
}

func (w *Writer) WriteContext(ctx context.Context, p []byte) (int, error) {
	writeChan := make(chan result, 1)

	go func() {
		written := 0
		total := len(p)
		for written < total {
			n, err := unix.Write(w.fd, p[written:])
			if err != nil {
				writeChan <- result{n: written, err: err}
				return
			}
			written += n
		}
		writeChan <- result{n: written, err: nil}
	}()

	select {
	case <-ctx.Done():
		// Close the file to unblock the goroutine stuck in unix.Write
		w.closeOnce()
		// Wait for the goroutine to actually exit (prevents leak)
		<-writeChan
		return 0, ctx.Err()
	case res := <-writeChan:
		if errors.Is(res.err, os.ErrDeadlineExceeded) {
			return 0, ctx.Err()
		}
		return res.n, res.err
	}
}
