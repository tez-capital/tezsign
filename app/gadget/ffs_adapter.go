package main

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const pollInterval = 100 * time.Millisecond

type Reader struct {
	fd int
}

type Writer struct {
	fd int
}

// FunctionFS can leave blocking reads and writes stuck in the kernel during
// early bring-up. Use nonblocking descriptors plus poll so cancellation stays
// explicit and we do not leak a goroutine per pending syscall.

func NewReader(f *os.File) (*Reader, error) {
	fd := int(f.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &Reader{fd: fd}, nil
}

func NewWriter(f *os.File) (*Writer, error) {
	fd := int(f.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	return &Writer{fd: fd}, nil
}

func (r *Reader) ReadContext(ctx context.Context, p []byte) (int, error) {
	for {
		n, err := unix.Read(r.fd, p)
		switch {
		case err == nil:
			if n == 0 {
				return 0, io.EOF
			}
			return n, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			if err := waitForFD(ctx, r.fd, unix.POLLIN); err != nil {
				return 0, err
			}
		default:
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return 0, ctx.Err()
			}
			return n, err
		}
	}
}

func (w *Writer) WriteContext(ctx context.Context, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	written := 0
	for written < len(p) {
		n, err := unix.Write(w.fd, p[written:])
		switch {
		case n > 0:
			written += n
		case err == nil:
			return written, io.ErrNoProgress
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			if err := waitForFD(ctx, w.fd, unix.POLLOUT); err != nil {
				return written, err
			}
		default:
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return written, ctx.Err()
			}
			return written, err
		}
	}

	return written, nil
}

func waitForFD(ctx context.Context, fd int, events int16) error {
	pollFDs := []unix.PollFd{{Fd: int32(fd), Events: events}}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, err := unix.Poll(pollFDs, pollTimeoutMillis(ctx))
		switch {
		case err == nil:
		case errors.Is(err, unix.EINTR):
			continue
		default:
			return err
		}

		if n == 0 {
			continue
		}

		revents := pollFDs[0].Revents
		switch {
		case revents&unix.POLLNVAL != 0:
			return unix.EBADF
		case revents&events != 0:
			return nil
		case revents&unix.POLLHUP != 0:
			return io.EOF
		case revents&unix.POLLERR != 0:
			return unix.EIO
		}
	}
}

func pollTimeoutMillis(ctx context.Context) int {
	timeout := pollInterval
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0
		}
		if remaining < timeout {
			timeout = remaining
		}
	}

	ms := int(timeout / time.Millisecond)
	if ms == 0 {
		return 1
	}
	return ms
}
