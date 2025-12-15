package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func watchLiveness(sockPath string, ready *atomic.Uint32, l *slog.Logger) {
	for {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			ready.Store(0)
			l.Debug("gadget not ready (socket down), retrying", "err", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		l.Info("connected to gadget liveness socket")
		ready.Store(1)
		// Block until the socket dies, then loop.
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
		ready.Store(0)
		l.Warn("lost liveness socket; marking not ready")
	}
}

func serveEnabled(ctx context.Context, sockPath string, l *slog.Logger) <-chan struct{} {
	closeCompletedChan := make(chan struct{})
	go func() {
		defer func() {
			closeCompletedChan <- struct{}{}
			close(closeCompletedChan)
		}()

		l.Info("starting enabled socket server", "socket", sockPath)
		// Remove stale socket file if exists
		_ = os.Remove(sockPath)

		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			l.Error("failed to start enabled socket server", "err", err.Error())
			return
		}
		defer ln.Close()

		// world-readable is fine; it's just liveness
		_ = os.Chmod(sockPath, 0666)

		// Track active connections for clean shutdown
		var connMu sync.Mutex
		conns := make(map[net.Conn]struct{})

		// Close listener when context is cancelled
		go func() {
			<-ctx.Done()
			ln.Close()
			// Close all active connections
			connMu.Lock()
			for c := range conns {
				c.Close()
			}
			connMu.Unlock()
		}()

		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					l.Info("enabled socket server shutting down")
					return
				default:
					l.Error("failed to accept enabled socket connection", "err", err)
					continue
				}
			}

			connMu.Lock()
			conns[conn] = struct{}{}
			connMu.Unlock()

			go func(c net.Conn) {
				defer func() {
					c.Close()
					connMu.Lock()
					delete(conns, c)
					connMu.Unlock()
				}()
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	return closeCompletedChan
}

func runEnabledWatcher(enabled <-chan bool, sockPath string, l *slog.Logger) {
	var cancel context.CancelFunc
	var closeCompletedChan <-chan struct{}
	isEnabled := false
	for en := range enabled {
		if en && !isEnabled {
			isEnabled = true
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			closeCompletedChan = serveEnabled(ctx, sockPath, l)
		} else if !en && isEnabled {
			isEnabled = false
			if cancel != nil {
				cancel()
				cancel = nil
				<-closeCompletedChan // wait for close
			}
			// Clean up socket file
			_ = os.Remove(sockPath)
		}
	}
}
