package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/samber/lo"
	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/keychain"
	"github.com/tez-capital/tezsign/signer"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func cmdListDevices() *cli.Command {
	return &cli.Command{
		Name:            "list-devices",
		Usage:           "List available USB FunctionFS gadgets",
		SkipFlagParsing: true,
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			l := h.Log
			infos, err := common.ListFFSDevices(l)
			if err != nil {
				return err
			}
			if !isTTY(os.Stdout) {
				return json.NewEncoder(os.Stdout).Encode(infos)
			}
			if len(infos) == 0 {
				fmt.Printf("No devices. Looking for VID=%04x PID=%04x\n", common.VID, common.PID)
				return nil
			}
			for i, inf := range infos {
				fmt.Printf("%d) serial=%s manufacturer=%s product=%s\n", i, inf.Serial, inf.Manufacturer, inf.Product)
			}
			return nil
		},
	}
}

func cmdInit() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Initialize master store & seed on gadget",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "deterministic",
				Usage: "Enable deterministic HD mode (EIP-2333 path)",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker
			l := h.Log

			info, err := common.ReqInitInfo(b)
			if err != nil {
				return fmt.Errorf("init: query: %w", err)
			}

			if info.GetMasterPresent() {
				mode := "random-only"
				if info.GetDeterministicEnabled() {
					mode = "deterministic"
				}
				fmt.Printf("Already initialized (%s mode).\n", mode)
				return nil
			}

			pass, err := obtainPassword("Master passphrase", false)
			if err != nil {
				return fmt.Errorf("init master: %w", err)
			}
			defer keychain.MemoryWipe(pass)

			confirm, err := obtainPassword("Confirm master passphrase", false)
			if err != nil {
				return fmt.Errorf("init master: %w", err)
			}
			defer keychain.MemoryWipe(confirm)

			if subtle.ConstantTimeCompare(pass, confirm) != 1 {
				return fmt.Errorf("passphrases do not match")
			}

			ok, err := common.ReqInitMaster(b, c.Bool("deterministic"), pass)
			if err != nil {
				l.Warn("init master", slog.Any("err", err))
			} else if ok {
				fmt.Println("Master initialized.")
			}
			return nil
		},
	}
}

func cmdRun() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Connect to gadget; optionally start a small HTTP signing server",
		ArgsUsage: "[alias1 alias2 ...]  # optional list of key IDs to unlock (TEZSIGN_UNLOCK_KEYS env overrides, else all keys)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "listen",
				Usage: fmt.Sprintf("HTTP listen address (default port %s). If empty, no server is started.", defaultPort),
			},
			&cli.BoolFlag{
				Name:  "no-retry",
				Usage: "Exit with non-zero on disconnect instead of auto-retrying",
				Value: false,
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			l := h.Log

			var current atomic.Value // of type *cur
			current.Store(&cur{sess: h.Session})

			getBroker := func() *broker.Broker {
				v := current.Load().(*cur)
				return v.sess.Broker
			}

			// Discover keys. If none, there’s no point in running.
			// (we need their states to validate the allowed keys).
			st, err := common.ReqStatus(getBroker())
			if err != nil {
				return fmt.Errorf("run: status: %w", err)
			}
			if st == nil || len(st.GetKeys()) == 0 {
				return ErrDeviceHasNoKeys
			}

			// Build allow-list from env or args; if empty, allow ALL existing keys
			allow := resolveKeysFromEnvOrArgs(c.Args().Slice())
			if len(allow) == 0 {
				allow = make([]string, 0, len(st.GetKeys()))
				for _, k := range st.GetKeys() {
					allow = append(allow, k.GetKeyId())
				}
				if len(allow) == 0 {
					return ErrDeviceHasNoKeys
				}
				l.Info("no aliases provided; allowing all keys from device", slog.Any("keys", allow))
			}

			allowSet := make(map[string]struct{}, len(allow))

			// Index status for quick lookups and verify unlocked
			known := make(map[string]*signer.KeyStatus, len(st.GetKeys()))
			locked := []string{}
			missing := []string{}
			for _, k := range st.GetKeys() {
				known[k.GetKeyId()] = k
			}
			for _, a := range allow {
				ks, ok := known[a]
				if !ok {
					missing = append(missing, a)
					continue
				}
				if ks.GetLockState().String() != "UNLOCKED" {
					locked = append(locked, a)
				}
				allowSet[ks.GetTz4()] = struct{}{}
			}
			if len(missing) > 0 {
				return fmt.Errorf("one or more invalid key aliases provided: %s", strings.Join(missing, ", "))
			}
			if len(locked) > 0 {
				// Don’t hard-fail. HTTP /sign will 403 on locked keys anyway.
				l.Warn("some allowed keys are locked; requests for them will be denied",
					slog.String("locked", strings.Join(locked, ", ")))
			}

			addr := c.String("listen")
			noRetry := c.Bool("no-retry")

			// Only start HTTP if --listen was provided at all
			if !c.IsSet("listen") {
				fmt.Println("Connected; no --listen provided. Press Ctrl+C to quit.")
				return runWatchdog(ctx, &current, h, noRetry)
			}

			if _, _, err := net.SplitHostPort(addr); err != nil {
				addr = net.JoinHostPort(addr, defaultPort)
			}

			// Start HTTP server with allow-list
			app := buildFiberApp(getBroker, l, allowSet)

			httpErrCh := make(chan error, 1)
			go func() {
				l.Debug("HTTP server listening", slog.String("addr", addr))
				if err := app.Listen(addr); err != nil {
					httpErrCh <- err
				}
			}()

			// watchdog runs alongside HTTP; if it exits with error, stop HTTP and bubble up
			wdErrCh := make(chan error, 1)
			go func() {
				if err := runWatchdog(ctx, &current, h, noRetry); err != nil {
					wdErrCh <- err
				} else {
					wdErrCh <- nil
				}
			}()

			// graceful shutdown + watchdog
			sigCh := make(chan os.Signal, 2)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			select {
			case <-sigCh:
				ctxTO, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = app.ShutdownWithContext(ctxTO)
				return nil
			case err := <-httpErrCh:
				return err
			case err := <-wdErrCh:
				ctxTO, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = app.ShutdownWithContext(ctxTO)
				return err
			}
		},
	}
}

func cmdList() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List keys in the gadget store",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			st, err := common.ReqStatus(b)
			if err != nil {
				return err
			}
			if st == nil || len(st.GetKeys()) == 0 {
				if !isTTY(os.Stdout) {
					return json.NewEncoder(os.Stdout).Encode([]string{})
				}

				fmt.Println("No keys.")
				return nil
			}

			// collect aliases (keys) and tz4 only
			keys := make(map[string]string, len(st.GetKeys()))
			for _, k := range st.GetKeys() {
				keys[k.GetKeyId()] = k.GetTz4()
			}

			if !isTTY(os.Stdout) {
				return json.NewEncoder(os.Stdout).Encode(keys)
			}

			// Render as chips/bubbles
			w, _, _ := term.GetSize(int(os.Stdout.Fd()))
			if w <= 0 {
				w = 80
			}
			fmt.Println(renderAliasChips(lo.Keys(keys), w))

			return nil
		},
	}
}

func cmdNewKeys() *cli.Command {
	return &cli.Command{
		Name:      "new",
		Usage:     "Create one or more keys (deterministic if seed enabled)",
		ArgsUsage: "[alias1 alias2 ...]  (no args => one auto-assigned key)",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			pass, err := obtainPassword("Master passphrase", false)
			if err != nil {
				return fmt.Errorf("new keys: %w", err)
			}
			defer keychain.MemoryWipe(pass)

			keys := c.Args().Slice()

			results, err := common.ReqNewKeys(b, keys, pass)
			if err != nil {
				return err
			}

			failed := 0
			for _, r := range results {
				if r.GetOk() {
					fmt.Printf("OK   id=%s  tz4=%s  BLpk=%s\n", r.GetKeyId(), r.GetTz4(), r.GetBlPubkey())
				} else {
					fmt.Printf("FAIL id=%s  err=%s\n", r.GetKeyId(), r.GetError())
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("failed to create %d/%d key(s)", failed, len(results))
			}
			return nil
		},
	}
}

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show status; optionally filter by keys",
		ArgsUsage: "[alias1 alias2 ...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "full",
				Usage: "Disable styling and print pubkey and PoP",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			st, err := common.ReqStatus(b)
			if err != nil {
				return err
			}
			filter := map[string]bool{}
			for _, k := range c.Args().Slice() {
				filter[k] = true
			}

			if !isTTY(os.Stdout) {
				var out []keyStatusJSON
				if st != nil {
					for _, ks := range st.GetKeys() {
						if len(filter) > 0 && !filter[ks.GetKeyId()] {
							continue
						}
						out = append(out, getKeysStatusJSON(ks))
					}
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

			if c.Bool("full") {
				for _, k := range st.GetKeys() {
					if len(filter) > 0 && !filter[k.GetKeyId()] {
						continue
					}

					state := k.GetLockState().String()
					if k.GetStateCorrupted() {
						state = "CORRUPTED"
					}

					fmt.Printf("%s  [%s]\n", k.GetKeyId(), state)
					fmt.Printf("  tz4:       %s\n", k.GetTz4())
					fmt.Printf("  BLpk:      %s\n", k.GetBlPubkey())
					fmt.Printf("  PoP(BLsig): %s\n", k.GetPop())
					fmt.Printf("  last block:        level=%d round=%d\n", k.GetLastBlockLevel(), k.GetLastBlockRound())
					fmt.Printf("  last preattest.:   level=%d round=%d\n", k.GetLastPreattestationLevel(), k.GetLastPreattestationRound())
					fmt.Printf("  last attest.:      level=%d round=%d\n", k.GetLastAttestationLevel(), k.GetLastAttestationRound())
				}

				return nil
			}

			// TTY: bordered table with fixed-width columns
			fmt.Println(renderStatusTable(statusRows(st.GetKeys()), statusTableOpts{Selectable: false, Cursor: -1}))
			return nil
		},
	}
}

func cmdUnlockKeys() *cli.Command {
	return &cli.Command{
		Name:      "unlock",
		Usage:     "Unlock one or more keys",
		ArgsUsage: "[alias1 alias2 ...] (or env TEZSIGN_UNLOCK_KEYS). If none provided user will have to select",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			keys := resolveKeysFromEnvOrArgs(c.Args().Slice())

			// If interactive TTY and no keys -> open picker to choose keys
			if len(keys) == 0 && isTTY(os.Stdout) {
				chosen, aborted, err := runKeyPicker(b)
				if err != nil {
					return err
				}
				if aborted {
					return ErrAborted
				}
				if len(chosen) == 0 {
					return ErrNoKeysSelected
				}
				keys = chosen
			}

			pass, err := obtainPassword("Unlock passphrase", true)
			if err != nil {
				if errors.Is(err, ErrEmptyPassphrase) && len(keys) == 0 {
					// Silent success if no keys to unlock and empty passphrase
					return nil
				}
				return fmt.Errorf("unlock keys: %w", err)
			}
			defer keychain.MemoryWipe(pass)

			if len(keys) == 0 {
				st, err := common.ReqStatus(b)
				if err != nil {
					return err
				}
				keys = lo.Map(st.GetKeys(), func(key *signer.KeyStatus, _ int) string {
					return key.GetKeyId()
				})
			}

			res, err := common.ReqUnlockKeys(b, keys, pass)
			if err != nil {
				return err
			}

			if !isTTY(os.Stdout) {
				out := make([]keyStateJSON, 0, len(res))
				for _, r := range res {
					o := keyStateJSON{ID: r.GetKeyId(), OK: r.GetOk()}
					if !r.GetOk() {
						o.Err = r.GetError()
					}
					out = append(out, o)
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

			okLabels := make([]string, 0, len(res))
			errLabels := make([]string, 0)
			for _, r := range res {
				if r.GetOk() {
					okLabels = append(okLabels, r.GetKeyId()+" ✓")
				} else {
					errLabels = append(errLabels, r.GetKeyId()+" ✗")
				}
			}

			w, _, _ := term.GetSize(int(os.Stdout.Fd()))
			if len(okLabels) > 0 {
				fmt.Println(renderChips(okLabels, chipOkStyle, w))
			}
			if len(errLabels) > 0 {
				if len(okLabels) > 0 {
					fmt.Println()
				}
				fmt.Println(renderChips(errLabels, chipErrStyle, w))
			}

			return nil
		},
	}
}

func cmdLockKeys() *cli.Command {
	return &cli.Command{
		Name:      "lock",
		Usage:     "Lock one or more keys",
		ArgsUsage: "[alias1 alias2 ...] If none provided user will have to select",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			keys := c.Args().Slice()

			// If interactive TTY and no keys -> open picker to choose keys
			if len(keys) == 0 && isTTY(os.Stdout) {
				chosen, aborted, err := runKeyPicker(b)
				if err != nil {
					return err
				}
				if aborted {
					return ErrAborted
				}
				if len(chosen) == 0 {
					return ErrNoKeysSelected
				}
				keys = chosen
			}

			if len(keys) == 0 {
				st, err := common.ReqStatus(b)
				if err != nil {
					return err
				}
				if st == nil || len(st.GetKeys()) == 0 {
					return nil
				}
				keys = lo.Map(st.GetKeys(), func(key *signer.KeyStatus, _ int) string {
					return key.GetKeyId()
				})
			}

			res, err := common.ReqLockKeys(b, keys)
			if err != nil {
				return err
			}

			if !isTTY(os.Stdout) {
				out := make([]keyStateJSON, 0, len(res))
				for _, r := range res {
					o := keyStateJSON{ID: r.GetKeyId(), OK: r.GetOk()}
					if !r.GetOk() {
						o.Err = r.GetError()
					}
					out = append(out, o)
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

			okLabels := make([]string, 0, len(res))
			errLabels := make([]string, 0)
			for _, r := range res {
				if r.GetOk() {
					okLabels = append(okLabels, r.GetKeyId()+" ✓")
				} else {
					errLabels = append(errLabels, r.GetKeyId()+" ✗")
				}
			}

			w, _, _ := term.GetSize(int(os.Stdout.Fd()))
			if len(okLabels) > 0 {
				fmt.Println(renderChips(okLabels, chipOkStyle, w))
			}
			if len(errLabels) > 0 {
				if len(okLabels) > 0 {
					fmt.Println()
				}
				fmt.Println(renderChips(errLabels, chipErrStyle, w))
			}

			return nil
		},
	}
}

func cmdDeleteKeys() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Delete one or more keys from the gadget (requires master passphrase)",
		ArgsUsage: "alias1 alias2 ...",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			b := h.Session.Broker

			keys := c.Args().Slice()

			// If interactive TTY and no keys -> open picker to choose keys
			if len(keys) == 0 && isTTY(os.Stdout) {
				chosen, aborted, err := runKeyPicker(b)
				if err != nil {
					return err
				}
				if aborted {
					return ErrAborted
				}
				if len(chosen) == 0 {
					return ErrNoKeysSelected
				}
				keys = chosen
			}

			pass, err := obtainPassword("Master passphrase", false)
			if err != nil {
				if errors.Is(err, ErrEmptyPassphrase) && len(keys) == 0 {
					// Silent success if no keys to delete and empty passphrase
					return nil
				}
				return fmt.Errorf("delete keys: %w", err)
			}
			defer keychain.MemoryWipe(pass)

			res, err := common.ReqDeleteKeys(b, keys, pass)
			if err != nil {
				return err
			}

			if !isTTY(os.Stdout) {
				out := make([]keyStateJSON, 0, len(res))
				for _, r := range res {
					o := keyStateJSON{ID: r.GetKeyId(), OK: r.GetOk()}
					if !r.GetOk() {
						o.Err = r.GetError()
					}
					out = append(out, o)
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

			okLabels := make([]string, 0, len(res))
			errLabels := make([]string, 0)
			for _, r := range res {
				if r.GetOk() {
					okLabels = append(okLabels, r.GetKeyId()+" ✓")
				} else {
					errLabels = append(errLabels, r.GetKeyId()+" ✗")
				}
			}

			w, _, _ := term.GetSize(int(os.Stdout.Fd()))
			if len(okLabels) > 0 {
				fmt.Println(renderChips(okLabels, chipOkStyle, w))
			}
			if len(errLabels) > 0 {
				if len(okLabels) > 0 {
					fmt.Println()
				}
				fmt.Println(renderChips(errLabels, chipErrStyle, w))
			}

			return nil
		},
	}
}

func cmdAdvanced() *cli.Command {
	return &cli.Command{
		Name:  "advanced",
		Usage: "Advanced / low-level maintenance commands",
		Commands: []*cli.Command{
			withBefore(cmdUSBPortReset(), withLoggerOnly()),
			withBefore(cmdSetLevel(), withLoggerOnly()), // IMPORTANT: do NOT use withSession here

		},
	}
}

func cmdUSBPortReset() *cli.Command {
	return &cli.Command{
		Name:  "usb-port-reset",
		Usage: "Force a USB port reset on the gadget (it will re-enumerate)",
		Action: func(ctx context.Context, c *cli.Command) error {
			h := mustHost(ctx)
			serial := c.String("device") // global flag
			if err := common.ResetDevice(serial, h.Log); err != nil {
				return err
			}

			fmt.Println("OK: USB reset sent. The device will re-enumerate.")

			return nil
		},
	}
}

func cmdSetLevel() *cli.Command {
	return &cli.Command{
		Name:      "set-level",
		Usage:     "Set level for a key alias (round will be reset to 0)",
		ArgsUsage: "<alias> <level>",
		Action: func(ctx context.Context, c *cli.Command) error {
			args := c.Args().Slice()
			if len(args) < 2 {
				return fmt.Errorf("usage: set-level <alias> <level>")
			}

			keyID := strings.TrimSpace(args[0])
			lvlStr := strings.TrimSpace(args[1])

			if keyID == "" {
				return fmt.Errorf("empty alias")
			}
			level, err := strconv.ParseUint(lvlStr, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid level %q: %w", lvlStr, err)
			}

			h := mustHost(ctx)
			l := h.Log

			devSerial := c.String("device")
			sess, err := common.Connect(common.ConnectParams{
				Serial:  devSerial,
				Logger:  l,
				Channel: common.ChanMgmt,
			})
			if err != nil {
				return err
			}
			defer sess.Close()

			b := sess.Broker

			ok, err := common.ReqSetLevel(b, keyID, level)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("set-level failed: %v", err)
			}

			fmt.Printf("OK: %s level set to %d (round reset to 0)\n", keyID, level)
			return nil
		},
	}
}
