package main

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/tez-capital/tezsign/app/gadget/common"
)

func triggerSoftConnect(l *slog.Logger) error {
	const udcClassPath = "/sys/class/udc"
	const softConnectFile = "soft_connect"

	l.Info("--- Checking %s for %s ---", udcClassPath, softConnectFile)

	softConnectPath := "/tmp/soft_connect"
	if _, err := os.Stat(softConnectPath); err != nil {
		l.Error("soft_connect symlink missing", "err", err.Error())
		return err
	}
	l.Info("triggering soft connect", "udc", softConnectPath)
	err := os.WriteFile(softConnectPath, []byte("disconnect\n"), 0644) // writing '0' disconnects, '1' connects
	if err != nil {
		l.Error("writing soft_connect file", "err", err.Error())
		return err
	}
	time.Sleep(500 * time.Millisecond)

	err = os.WriteFile(softConnectPath, []byte("connect\n"), 0644) // writing '0' disconnects, '1' connects
	if err != nil {
		l.Error("writing soft_connect file", "err", err.Error())
		return err
	}
	l.Info("soft connect triggered successfully")
	return nil
}

func drainEP0Events(ep0 *os.File, enabled chan<- bool, ready *atomic.Uint32, l *slog.Logger) {
	buf := make([]byte, evSize)

	for {
		n, err := ep0.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			l.Error("ep0 read events", "err", err)
			triggerSoftConnect(l) // try to recover by soft reconnect
			continue
		}

		if n < evSize {
			l.Warn("ep0 read event too short", "n", n)
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		//                      ^ evType
		evType := int(buf[8])
		switch evType {
		case evTypeBind:
			l.Info("tezsign gadget bound")
			continue
		case evTypeUnbind:
			l.Info("tezsign gadget unbound")
			continue
		case evTypeEnable:
			l.Info("tezsign gadget enabled")
			enabled <- true
			continue
		case evTypeDisable:
			l.Info("tezsign gadget disabled")
			enabled <- false
			triggerSoftConnect(l)
			continue
		case evTypeSuspend:
			enabled <- false
			l.Info("tezsign gadget suspended")
			continue
		case evTypeResume:
			enabled <- true
			l.Info("tezsign gadget resumed")
			continue
		case evTypeSetup:
			// Handle below
		default:
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		// ^____ request ____^
		req := parseCtrlReq(buf[0:8])
		l.Info("parsed", "type", req.bmRequestType, "request", req.bRequest, "length", req.wLength)
		// Handle our vendor IN request
		if req.bmRequestType == bmReqTypeVendorIn && req.bRequest == vendorReqReady {
			// Prepare reply
			reply := [8]byte{}
			copy(reply[:4], []byte("TZSG"))
			binary.LittleEndian.PutUint16(reply[4:6], protoVersion)
			reply[6] = byte(ready.Load())

			// Respect host's wLength (shorter read is OK)
			wlen := int(req.wLength)
			if wlen > len(reply) {
				wlen = len(reply)
			}
			// Write data stage
			if _, err := ep0.Write(reply[:wlen]); err != nil {
				l.Error("ep0 write vendor reply", "err", err)
			}
			continue
		}
		l.Warn("Unhandled SETUP request, STALLING", "type", req.bmRequestType, "req", req.bRequest)
		// A 0-byte write on an unhandled request is the
		// userspace way to signal a STALL to the kernel.
		if _, err := ep0.Write(nil); err != nil {
			l.Error("ep0 write ZLP/STALL failed", "err", err)
		}
	}
}

func main() {
	l := slog.Default()

	ep0, err := os.OpenFile(Ep0Path, os.O_RDWR, 0)
	if err != nil {
		slog.Error("failed to open ep0", "error", err.Error(), "function", FunctionName, "ffs_root", common.FfsInstanceRoot)
		os.Exit(1)
	}
	defer ep0.Close()

	if _, err := ep0.Write(deviceDescriptors); err != nil {
		slog.Error("failed to write device descriptors", "error", err.Error())
		os.Exit(1)
	}

	if _, err := ep0.Write(deviceStrings); err != nil {
		slog.Error("failed to write device strings", "error", err.Error())
		os.Exit(1)
	}

	// Start watching gadget liveness
	var ready atomic.Uint32
	go watchLiveness(common.ReadySock, &ready, l)
	enabled := make(chan bool, 1)
	go runEnabledWatcher(enabled, common.EnabledSock, l)

	l.Info("FFS registrar online; handling EP0 control & events")

	drainEP0Events(ep0, enabled, &ready, l)
}
