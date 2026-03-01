package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/logging"
	"github.com/urfave/cli/v3"
)

// session accessor that survives reconnection
type cur struct {
	sess *common.Session
}

func resolveKeysFromEnvOrArgs(args []string) []string {
	if v := strings.TrimSpace(os.Getenv(envKeys)); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	// fall back to args
	if len(args) > 0 {
		return args
	}
	return nil
}

func mustHost(ctx context.Context) *HostContext {
	v := ctx.Value(hostCtxKey{})
	if v == nil {
		panic("host context missing (session not initialized)")
	}
	return v.(*HostContext)
}

func runWatchdog(ctx context.Context, curRef *atomic.Value, initial *HostContext, noRetry bool) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			v := curRef.Load().(*cur)

			// Probe EP0 vendor ready
			idx := uint16(0)
			if v.sess != nil && v.sess.Intf != nil {
				idx = uint16(v.sess.Intf.Setting.Number)
			}
			ok, err := common.VendorReadyInInterface(v.sess.Dev, common.VendorReqReady, idx, v.sess.Log)

			if ok && err == nil {
				continue
			}

			// Not ready or errored → handle policy
			if noRetry {
				// Exit non-zero by returning an error
				if err != nil {
					v.sess.Log.Error("gadget disconnected", slog.Any("err", err))
					return fmt.Errorf("gadget disconnected: %w", err)
				}
				v.sess.Log.Error("gadget not ready; exiting (--no-retry)")
				return fmt.Errorf("gadget not ready")
			}

			if err != nil {
				v.sess.Log.Warn("gadget not ready, attempting reconnect...", slog.Any("err", err))
			} else {
				v.sess.Log.Warn("gadget not ready, attempting reconnect...")
			}

			oldSess := v.sess

			// Try indefinitely until success or context cancelled
			params := common.ConnectParams{
				Serial:    oldSess.Serial,
				Logger:    initial.Log,
				Channel:   oldSess.Channel,
				KeepAlive: oldSess.KeepAlive,
			}
			oldSess.Close()

			s, rerr := tryReconnect(ctx, params)
			if rerr != nil {
				return rerr
			}
			v.sess.Log.Info("reconnected", slog.String("serial", s.Serial))
			curRef.Store(&cur{sess: s})
		}
	}
}

func tryReconnect(ctx context.Context, p common.ConnectParams) (*common.Session, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			if s, err := common.Connect(p); err == nil {
				return s, nil
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// shared before-hook that opens USB on a specific channel
func withSession(channel common.Channel) func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	return func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		logCfg := logging.NewConfigFromEnv()
		if err := logging.EnsureDir(logCfg.File); err != nil {
			panic("Could not create dir for path of configuration file!")
		}
		l, _ := logging.New(logCfg)

		h := &HostContext{Log: l}

		devSerial := cmd.String("device")
		keepAlive := time.Duration(0)
		if cmd.Name == "run" && cmd.IsSet("keep-alive") {
			keepAlive = cmd.Duration("keep-alive")
			if keepAlive < minKeepAlive {
				return ctx, fmt.Errorf("--keep-alive must be at least %s", minKeepAlive)
			}
		}

		l.Debug("opening USB", slog.String("cmd", cmd.Name), slog.String("channel", map[common.Channel]string{
			common.ChanSign: "sign",
			common.ChanMgmt: "mgmt",
		}[channel]))

		sess, err := common.Connect(common.ConnectParams{
			Serial:    devSerial,
			Logger:    l,
			Channel:   channel,
			KeepAlive: keepAlive,
		})
		if err != nil {
			return ctx, err
		}
		l.Debug("connected", slog.String("serial", sess.Serial))
		h.Session = sess

		return context.WithValue(ctx, hostCtxKey{}, h), nil
	}
}

func withLoggerOnly() func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	return func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		logCfg := logging.NewConfigFromEnv()
		if logCfg.File == "" {
			logCfg.File = logging.DefaultFileInExecDir(logFileName)
		}
		if err := logging.EnsureDir(logCfg.File); err != nil {
			return ctx, fmt.Errorf("log dir: %w", err)
		}
		l, _ := logging.New(logCfg)
		h := &HostContext{Log: l} // Session stays nil
		return context.WithValue(ctx, hostCtxKey{}, h), nil
	}
}

// shared after-hook to close the session if present
func closeSession(ctx context.Context, _ *cli.Command) error {
	if v := ctx.Value(hostCtxKey{}); v != nil {
		h := v.(*HostContext)
		if h.Session != nil {
			h.Log.Debug("closing session")
			h.Session.Close()
		}
	}
	return nil
}

func withBefore(c *cli.Command, before cli.BeforeFunc) *cli.Command {
	c.Before = before
	return c
}
