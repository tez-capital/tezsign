package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// pollInterval is how often we check context cancellation during I/O
	pollInterval = 100 * time.Millisecond
)

// Reader wraps a file descriptor for context-aware reading using poll.
// Unlike the previous implementation, goroutines properly terminate when
// context is canceled.
type Reader struct {
	fd     int
	mu     sync.Mutex
	closed bool
}

// Writer wraps a file descriptor for context-aware writing using poll.
type Writer struct {
	fd     int
	mu     sync.Mutex
	closed bool
}

func NewReader(f *os.File) (*Reader, error) {
	fd := int(f.Fd())
	// Set non-blocking mode so we can use poll
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &Reader{fd: fd}, nil
}

func NewWriter(f *os.File) (*Writer, error) {
	fd := int(f.Fd())
	// Set non-blocking mode so we can use poll
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &Writer{fd: fd}, nil
}

// ReadContext reads from the file descriptor with context cancellation support.
// Uses poll() to wait for data, periodically checking if context is done.
// This prevents goroutine leaks that occurred with the previous blocking implementation.
func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return 0, os.ErrClosed
	}

	pollFds := []unix.PollFd{
		{Fd: int32(r.fd), Events: unix.POLLIN},
	}
	timeoutMs := int(pollInterval.Milliseconds())

	for {
		// Check context before polling
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		// Poll with timeout so we can check context periodically
		n, err := unix.Poll(pollFds, timeoutMs)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue // Interrupted, retry
			}
			return 0, err
		}

		if n == 0 {
			// Timeout - check context and retry
			continue
		}

		// Check for errors on the fd
		if pollFds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return 0, unix.EIO
		}

		// Data available, do non-blocking read
		if pollFds[0].Revents&unix.POLLIN != 0 {
			nr, err := unix.Read(r.fd, p)
			if err != nil {
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
					continue // No data yet, retry poll
				}
				return 0, err
			}
			if nr == 0 {
				return 0, unix.EIO // EOF on device
			}
			return nr, nil
		}
	}
}

// WriteContext writes to the file descriptor with context cancellation support.
// Uses poll() to wait for write readiness, periodically checking if context is done.
func (w *Writer) WriteContext(ctx context.Context, p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, os.ErrClosed
	}

	if len(p) == 0 {
		return 0, nil
	}

	pollFds := []unix.PollFd{
		{Fd: int32(w.fd), Events: unix.POLLOUT},
	}
	timeoutMs := int(pollInterval.Milliseconds())

	written := 0
	total := len(p)

	for written < total {
		// Check context before polling
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		// Poll with timeout so we can check context periodically
		n, err := unix.Poll(pollFds, timeoutMs)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue // Interrupted, retry
			}
			return written, err
		}

		if n == 0 {
			// Timeout - check context and retry
			continue
		}

		// Check for errors on the fd
		if pollFds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return written, unix.EIO
		}

		// Ready for write
		if pollFds[0].Revents&unix.POLLOUT != 0 {
			nw, err := unix.Write(w.fd, p[written:])
			if err != nil {
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
					continue // Not ready, retry poll
				}
				return written, err
			}
			written += nw
		}
	}

	return written, nil
}

// Close marks the reader as closed. The underlying fd is managed by os.File.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// Close marks the writer as closed. The underlying fd is managed by os.File.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}
