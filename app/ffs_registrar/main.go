package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tez-capital/tezsign/app/gadget/common"
	"github.com/tez-capital/tezsign/logging"
)

const ep0ReadRetryDelay = time.Second

func drainEP0Events(ep0 *os.File, ready *atomic.Uint32, l *slog.Logger) {
	buf := make([]byte, evSize)

	for {
		n, err := ep0.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			logging.Fatal(l, "ep0 read events failed; retrying", "err", err, "delay", ep0ReadRetryDelay)
			time.Sleep(ep0ReadRetryDelay)
			continue
		}

		if n < evSize {
			logging.Fatal(l, "ep0 read event too short", "n", n)
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		//                      ^ evType
		evType := int(buf[8])
		if evType != evTypeSetup {
			continue
		}

		// [65 90 0 0 0 0 8 0 | 4 | 0 0 0]
		// ^____ request ____^
		req := parseCtrlReq(buf[0:8])
		logging.All(l, "parsed", "type", req.bmRequestType, "request", req.bRequest, "length", req.wLength)

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
				logging.Fatal(l, "ep0 write vendor reply", "err", err)
			}
			continue
		}
		logging.Fatal(l, "Unhandled SETUP request, STALLING", "type", req.bmRequestType, "req", req.bRequest)
		// A 0-byte write on an unhandled request is the
		// userspace way to signal a STALL to the kernel.
		if _, err := ep0.Write(nil); err != nil {
			logging.Fatal(l, "ep0 write ZLP/STALL failed", "err", err)
		}
	}
}

func main() {
	logCfg := logging.NewConfigFromEnv()
	if logCfg.File == "" {
		dataStore := strings.TrimSpace(os.Getenv("DATA_STORE"))
		if dataStore != "" {
			if err := os.MkdirAll(dataStore, 0o700); err != nil {
				panic(fmt.Errorf("could not create DATA_STORE=%q: %w", dataStore, err))
			}
			logCfg.File = filepath.Join(dataStore, "registrar.log")
		} else {
			logCfg.File = logging.DefaultFileInExecDir("registrar.log")
		}
	}

	if err := logging.EnsureDir(logCfg.File); err != nil {
		panic("Could not create dir for path of configuration file!")
	}

	l, _ := logging.New(logCfg)

	l.Debug("logging to file", "path", logging.CurrentFile())

	ep0, err := os.OpenFile(Ep0Path, os.O_RDWR, 0)
	if err != nil {
		l.Error("failed to open ep0", "error", err.Error(), "function", FunctionName, "ffs_root", common.FfsInstanceRoot)
		os.Exit(1)
	}
	defer ep0.Close()

	if _, err := ep0.Write(deviceDescriptors); err != nil {
		l.Error("failed to write device descriptors", "error", err.Error())
		os.Exit(1)
	}

	if _, err := ep0.Write(deviceStrings); err != nil {
		l.Error("failed to write device strings", "error", err.Error())
		os.Exit(1)
	}

	// Start watching gadget liveness
	var ready atomic.Uint32
	go watchLiveness(common.ReadySock, &ready, l)

	l.Info("FFS registrar online; handling EP0 control & events")

	drainEP0Events(ep0, &ready, l)
}
