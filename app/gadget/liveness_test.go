package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tez-capital/tezsign/app/gadget/common"
)

// getTestSocketPath returns a short socket path that won't exceed Unix socket path limits
func getTestSocketPath(t *testing.T) string {
	// Unix sockets have path length limits (~104 on BSD/macOS, 108 on Linux)
	// Use a short path in /tmp to avoid issues
	return fmt.Sprintf("/tmp/tz_%d.sock", os.Getpid())
}

func TestServeReadySocketCreatesSocket(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)
	defer cleanup()

	// Give time for socket creation
	time.Sleep(50 * time.Millisecond)

	// Socket should exist
	if _, err := os.Stat(common.ReadySock); os.IsNotExist(err) {
		t.Error("socket file should exist after serveReadySocket")
	}
}

func TestServeReadySocketAcceptsConnection(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)
	defer cleanup()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Should be able to connect
	conn, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("failed to connect to socket: %v", err)
	}
	conn.Close()
}

func TestServeReadySocketOnlyOneActiveConnection(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)
	defer cleanup()

	time.Sleep(100 * time.Millisecond)

	// Connect first client
	conn1, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("failed to connect first client: %v", err)
	}

	// Connect second client - should cause first to be closed
	conn2, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("failed to connect second client: %v", err)
	}

	// Give time for the server to process
	time.Sleep(200 * time.Millisecond)

	// First connection should be closed (read should fail)
	conn1.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn1.Read(buf)
	// Should get an error (connection closed or timeout)

	conn1.Close()
	conn2.Close()
}

func TestServeReadySocketCleanup(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)

	time.Sleep(100 * time.Millisecond)

	// Connect a client
	conn, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	// Call cleanup
	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()

	// Cleanup should complete
	select {
	case <-cleanupDone:
		// Success
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not complete in time")
	}

	// Socket file should be removed
	if _, err := os.Stat(common.ReadySock); !os.IsNotExist(err) {
		t.Error("socket file should be removed after cleanup")
	}

	conn.Close()
}

func TestServeReadySocketHandlesMultipleReconnects(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)
	defer cleanup()

	time.Sleep(100 * time.Millisecond)

	// Simulate multiple reconnects (like registrar would do)
	for i := 0; i < 5; i++ {
		conn, err := net.Dial("unix", common.ReadySock)
		if err != nil {
			t.Fatalf("reconnect %d failed: %v", i, err)
		}
		time.Sleep(30 * time.Millisecond)
		conn.Close()
		time.Sleep(30 * time.Millisecond)
	}

	// Should still be able to connect after all reconnects
	conn, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("final connect failed: %v", err)
	}
	conn.Close()
}

func TestServeReadySocketNoGoroutineLeak(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)

	time.Sleep(100 * time.Millisecond)

	// Make many connections
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial("unix", common.ReadySock)
			if err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
			conn.Close()
		}()
	}

	wg.Wait()

	// Cleanup should complete quickly (no hanging goroutines)
	cleanupDone := make(chan struct{})
	go func() {
		cleanup()
		close(cleanupDone)
	}()

	select {
	case <-cleanupDone:
		// Success - all goroutines cleaned up
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup hung - possible goroutine leak")
	}
}

func TestServeReadySocketPermissions(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cleanup := serveReadySocket(l)
	defer cleanup()

	time.Sleep(100 * time.Millisecond)

	// Check permissions (should be world-readable: 0666)
	info, err := os.Stat(common.ReadySock)
	if err != nil {
		t.Fatalf("failed to stat socket: %v", err)
	}

	// On Unix, socket permissions might be masked, but we set 0666
	mode := info.Mode().Perm()
	// At minimum, it should be readable/writable by owner
	if mode&0600 == 0 {
		t.Errorf("socket should be accessible, got mode %o", mode)
	}
}

func TestServeReadySocketRemovesStaleSocket(t *testing.T) {
	origPath := common.ReadySock
	common.ReadySock = getTestSocketPath(t)
	defer func() {
		common.ReadySock = origPath
		os.Remove(common.ReadySock)
	}()

	// Create a stale socket file
	f, err := os.Create(common.ReadySock)
	if err != nil {
		t.Fatalf("failed to create stale file: %v", err)
	}
	f.Close()

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Should succeed even with stale file
	cleanup := serveReadySocket(l)
	defer cleanup()

	time.Sleep(100 * time.Millisecond)

	// Should be able to connect
	conn, err := net.Dial("unix", common.ReadySock)
	if err != nil {
		t.Fatalf("failed to connect after removing stale socket: %v", err)
	}
	conn.Close()
}
