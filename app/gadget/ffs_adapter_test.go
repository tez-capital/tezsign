package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func newPipeFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		t.Fatalf("unix.Pipe2: %v", err)
	}

	readerFile := os.NewFile(uintptr(fds[0]), fmt.Sprintf("reader-%d", fds[0]))
	writerFile := os.NewFile(uintptr(fds[1]), fmt.Sprintf("writer-%d", fds[1]))
	if readerFile == nil || writerFile == nil {
		if readerFile != nil {
			_ = readerFile.Close()
		} else {
			_ = unix.Close(fds[0])
		}
		if writerFile != nil {
			_ = writerFile.Close()
		} else {
			_ = unix.Close(fds[1])
		}
		t.Fatalf("os.NewFile returned nil")
	}

	return readerFile, writerFile
}

func TestReaderReadContextRespectsCancellation(t *testing.T) {
	readerFile, writerFile := newPipeFiles(t)
	defer readerFile.Close()
	defer writerFile.Close()

	reader, err := NewReader(readerFile)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err = reader.ReadContext(ctx, make([]byte, 8))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("read cancellation was too slow: %v", elapsed)
	}
}

func TestReaderReadContextReadsAvailableData(t *testing.T) {
	readerFile, writerFile := newPipeFiles(t)
	defer readerFile.Close()
	defer writerFile.Close()

	reader, err := NewReader(readerFile)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	want := []byte("tezsign")
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = writerFile.Write(want)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got := make([]byte, len(want))
	n, err := reader.ReadContext(ctx, got)
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if n != len(want) {
		t.Fatalf("expected %d bytes, got %d", len(want), n)
	}
	if string(got[:n]) != string(want) {
		t.Fatalf("expected %q, got %q", want, got[:n])
	}
}

func TestWriterWriteContextRespectsCancellationWhenNotWritable(t *testing.T) {
	readerFile, writerFile := newPipeFiles(t)
	defer readerFile.Close()
	defer writerFile.Close()

	writer, err := NewWriter(writerFile)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	fillPipe(t, int(writerFile.Fd()))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = writer.WriteContext(ctx, []byte("x"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}

func TestWriterWriteContextWritesAfterPeerDrains(t *testing.T) {
	readerFile, writerFile := newPipeFiles(t)
	defer readerFile.Close()
	defer writerFile.Close()

	writer, err := NewWriter(writerFile)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	fillPipe(t, int(writerFile.Fd()))

	go func() {
		time.Sleep(20 * time.Millisecond)
		buffer := make([]byte, 8192)
		_, _ = readerFile.Read(buffer)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	payload := []byte("tezsign")
	n, err := writer.WriteContext(ctx, payload)
	if err != nil {
		t.Fatalf("WriteContext: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("expected %d bytes, got %d", len(payload), n)
	}
}

func fillPipe(t *testing.T, fd int) {
	t.Helper()

	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatalf("get fd flags: %v", err)
	}

	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags|unix.O_NONBLOCK); err != nil {
		t.Fatalf("set nonblock: %v", err)
	}

	defer func() {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, flags); err != nil {
			t.Fatalf("restore fd flags: %v", err)
		}
	}()

	chunk := make([]byte, 4096)
	for {
		_, err := unix.Write(fd, chunk)
		switch {
		case err == nil:
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			return
		default:
			t.Fatalf("fill pipe: %v", err)
		}
	}
}
