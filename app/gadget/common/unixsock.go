package common

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type UnixListener struct {
	fd   int
	path string
}

func ListenUnix(path string, perm os.FileMode) (*UnixListener, error) {
	_ = os.Remove(path)

	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socket %q: %w", path, err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Close(fd)
			_ = os.Remove(path)
		}
	}()

	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		return nil, fmt.Errorf("bind %q: %w", path, err)
	}
	if err := os.Chmod(path, perm); err != nil {
		return nil, fmt.Errorf("chmod %q: %w", path, err)
	}
	if err := unix.Listen(fd, 16); err != nil {
		return nil, fmt.Errorf("listen %q: %w", path, err)
	}

	cleanup = false
	return &UnixListener{fd: fd, path: path}, nil
}

func (l *UnixListener) Accept() (*os.File, error) {
	fd, _, err := unix.Accept4(l.fd, unix.SOCK_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), l.path), nil
}

func (l *UnixListener) Close() error {
	return unix.Close(l.fd)
}

func DialUnix(path string) (*os.File, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socket %q: %w", path, err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = unix.Close(fd)
		}
	}()

	if err := unix.Connect(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		return nil, fmt.Errorf("connect %q: %w", path, err)
	}

	cleanup = false
	return os.NewFile(uintptr(fd), path), nil
}
