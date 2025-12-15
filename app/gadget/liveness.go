package main

import (
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/tez-capital/tezsign/app/gadget/common"
)

// serveReadySocket holds the socket open while the process is healthy.
// Registrar will connect and keep a single connection open.
// This implementation limits to one active connection at a time to prevent
// goroutine accumulation from repeated reconnections.
func serveReadySocket(l *slog.Logger) (cleanup func()) {
	_ = os.Remove(common.ReadySock) // stale
	ln, err := net.Listen("unix", common.ReadySock)
	if err != nil {
		l.Error("ready socket listen", "err", err, "path", common.ReadySock)
		return func() {}
	}
	// world-readable is fine; it's just liveness
	_ = os.Chmod(common.ReadySock, 0666)

	quit := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		l.Info("ready socket listening", "path", common.ReadySock)

		var currentConn net.Conn
		var connMu sync.Mutex

		for {
			// Set accept deadline to allow periodic quit checks
			if ul, ok := ln.(*net.UnixListener); ok {
				ul.SetDeadline(time.Now().Add(500 * time.Millisecond))
			}

			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-quit:
					// Close current connection on shutdown
					connMu.Lock()
					if currentConn != nil {
						currentConn.Close()
					}
					connMu.Unlock()
					return
				default:
					// Check if it's a timeout (expected)
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					l.Error("ready socket accept", "err", err)
					continue
				}
			}

			// Close previous connection if any (only one active at a time)
			connMu.Lock()
			if currentConn != nil {
				currentConn.Close()
			}
			currentConn = conn
			connMu.Unlock()

			// Handle this connection in a goroutine
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer func() {
					connMu.Lock()
					if currentConn == c {
						currentConn = nil
					}
					connMu.Unlock()
					c.Close()
				}()

				// Drain/discard with deadline checks
				buf := make([]byte, 1)
				for {
					// Set read deadline to allow periodic quit checks
					c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

					_, err := c.Read(buf)
					if err != nil {
						// Check if we should quit
						select {
						case <-quit:
							return
						default:
						}

						// Timeout is expected, continue
						if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
							continue
						}
						// Connection closed or error
						return
					}
				}
			}(conn)
		}
	}()

	return func() {
		close(quit)
		_ = ln.Close()
		wg.Wait()
		_ = os.Remove(common.ReadySock)
	}
}
