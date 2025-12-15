package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tez-capital/tezsign/app/gadget/common"
	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/keychain"
	"github.com/tez-capital/tezsign/logging"
	"github.com/tez-capital/tezsign/signer"
	"google.golang.org/protobuf/proto"
)

const (
	waitEndpointsTime = 30 * time.Second

	securedAttemptWindow = 30 * time.Second
	securedAttemptLimit  = 5
)

var securedRPCLimiter = newAttemptLimiter(securedAttemptLimit, securedAttemptWindow)

func main() {
	logCfg := logging.NewConfigFromEnv()
	if logCfg.File == "" {
		dataStore := strings.TrimSpace(os.Getenv("DATA_STORE"))
		if dataStore != "" {
			if err := os.MkdirAll(dataStore, 0o700); err != nil {
				panic(fmt.Errorf("could not create DATA_STORE=%q: %w", dataStore, err))
			}
			logCfg.File = filepath.Join(dataStore, "gadget.log")
		} else {
			logCfg.File = logging.DefaultFileInExecDir("gadget.log")
		}
	}

	if err := logging.EnsureDir(logCfg.File); err != nil {
		panic("Could not create dir for path of configuration file!")
	}

	l, _ := logging.New(logCfg)

	l.Debug("logging to file", "path", logging.CurrentFile())

	if err := run(l); err != nil {
		l.Error("RUN ERROR", slog.Any("err", err))
		os.Exit(1)
	}
}

func handleSignAndStatus(base func(context.Context, []byte) ([]byte, error)) broker.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req signer.Request
		if err := proto.Unmarshal(payload, &req); err != nil {
			return marshalErr(1, fmt.Sprintf("bad protobuf: %v", err)), nil
		}
		switch req.Payload.(type) {
		case *signer.Request_Sign, *signer.Request_Status:
			// allowed on IF0
		default:
			return marshalErr(98, "wrong interface: use management (IF1) for this request"), nil
		}

		return base(ctx, payload)
	}
}

func handleMgmtOnly(base func(context.Context, []byte) ([]byte, error)) broker.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req signer.Request
		if err := proto.Unmarshal(payload, &req); err != nil {
			return marshalErr(1, fmt.Sprintf("bad protobuf: %v", err)), nil
		}
		// reject Sign here
		if _, isSign := req.Payload.(*signer.Request_Sign); isSign {
			return marshalErr(1001, "wrong interface: use sign (IF0) for signing"), nil
		}
		return base(ctx, payload)
	}
}

func run(l *slog.Logger) error {
	l.Info("Waiting for endpoints...")
	in0, out0, in1, out1, err := waitForFunctionFSEndpoints(common.FfsInstanceRoot, waitEndpointsTime)
	if err != nil {
		return err
	}

	// IF0 (sign) endpoints
	in0Fd, err := os.OpenFile(in0, os.O_WRONLY, 0) // device -> host
	if err != nil {
		return fmt.Errorf("open IF0 IN: %w", err)
	}
	defer in0Fd.Close()
	out0Fd, err := os.OpenFile(out0, os.O_RDONLY, 0) // host -> device
	if err != nil {
		return fmt.Errorf("open IF0 OUT: %w", err)
	}
	defer out0Fd.Close()

	// IF1 (sign) endpoints
	in1Fd, err := os.OpenFile(in1, os.O_WRONLY, 0) // device -> host
	if err != nil {
		return fmt.Errorf("open IF1 IN: %w", err)
	}
	defer in1Fd.Close()
	out1Fd, err := os.OpenFile(out1, os.O_RDONLY, 0) // host -> device
	if err != nil {
		return fmt.Errorf("open IF1 OUT: %w", err)
	}
	defer out1Fd.Close()

	r0, _ := NewReader(out0Fd)
	w0, _ := NewWriter(in0Fd)
	r1, _ := NewReader(out1Fd)
	w1, _ := NewWriter(in1Fd)

	l.Info("Endpoints ready; starting broker",
		slog.String("IF0.in", in0), slog.String("IF0.out", out0),
		slog.String("IF1.in", in1), slog.String("IF1.out", out1),
	)

	// Keystore directory: DATA_STORE/keystore when DATA_STORE is set; else next to binary
	var baseDir string
	if ds := strings.TrimSpace(os.Getenv("DATA_STORE")); ds != "" {
		baseDir = filepath.Join(ds, "keystore")
	} else {
		baseDir = logging.DefaultFileInExecDir("keystore") // e.g. /path/to/bin/keystore
	}
	// Ensure keystore dir exists (0700 since it holds secrets)
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return fmt.Errorf("keystore mkdir %q: %w", baseDir, err)
	}

	fs, err := keychain.NewFileStore(baseDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	kr := keychain.NewKeyRing(l, fs)

	// --- broker handler: parse → validate → sign/deny → respond ---
	handleAll := func(ctx context.Context, payload []byte) ([]byte, error) {
		var req signer.Request
		if err := proto.Unmarshal(payload, &req); err != nil {
			return marshalErr(1, fmt.Sprintf("bad protobuf: %v", err)), nil
		}
		defer wipeReq(&req)
		defer keychain.MemoryWipe(payload)

		switch p := req.Payload.(type) {
		case *signer.Request_Unlock:
			pass := p.Unlock.GetPassphrase()
			defer keychain.MemoryWipe(pass)

			ids := p.Unlock.GetKeyIds()
			if len(ids) == 0 {
				return marshalErr(10, "unlock: no key_ids provided"), nil
			}
			if len(pass) == 0 {
				return marshalErr(11, "unlock: passphrase required"), nil
			}
			if ok, wait := securedRPCLimiter.Allow(); !ok {
				l.Warn("unlock throttled", slog.Duration("retry_in", wait))
				msg := fmt.Sprintf(
					"unlock throttled: retry in ~%s (max %d attempts per %s)",
					wait.Round(time.Second),
					securedAttemptLimit,
					securedAttemptWindow,
				)
				return marshalErr(rpcUnlockThrottled, msg), nil
			}

			results := make([]*signer.PerKeyResult, 0, len(ids))
			for _, id := range ids {
				res := &signer.PerKeyResult{KeyId: id}
				if err := kr.Unlock(id, pass); err != nil {
					res.Ok = false
					res.Error = err.Error()
					l.Error("unlock", "key", id, "err", err)
				} else {
					res.Ok = true
					l.Debug("UNLOCKED " + id)
				}
				results = append(results, res)
			}

			l.Debug("UNLOCK batch", "count", len(ids))

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_Unlock{
					Unlock: &signer.UnlockResponse{Results: results},
				},
			})

		case *signer.Request_Lock:
			ids := p.Lock.GetKeyIds()
			if len(ids) == 0 {
				return marshalErr(20, "lock: no key_ids provided"), nil
			}

			results := make([]*signer.PerKeyResult, 0, len(ids))
			for _, id := range ids {
				res := &signer.PerKeyResult{KeyId: id}
				if err := kr.Lock(id); err != nil {
					res.Ok = false
					res.Error = err.Error()
					l.Error("LOCK", "key", id, "err", err)
				} else {
					res.Ok = true
					l.Debug("LOCKED " + id)
				}
				results = append(results, res)
			}

			l.Debug("LOCK batch", "count", len(ids))

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_Lock{
					Lock: &signer.LockResponse{Results: results},
				},
			})

		case *signer.Request_Status:
			st := kr.Status()

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_Status{
					Status: &signer.StatusResponse{Keys: st},
				},
			})

		case *signer.Request_Sign:
			tz4 := p.Sign.GetTz4()
			sig, err := kr.SignAndUpdate(tz4, p.Sign.GetMessage())
			if err != nil {
				switch {
				case errors.Is(err, keychain.ErrKeyLocked):
					return marshalErr(rpcKeyLocked, keychain.ErrKeyLocked.Error()), nil
				case errors.Is(err, keychain.ErrKeyNotFound):
					return marshalErr(rpcKeyNotFound, keychain.ErrKeyNotFound.Error()), nil
				case errors.Is(err, keychain.ErrStaleWatermark):
					return marshalErr(rpcStaleWatermark, keychain.ErrStaleWatermark.Error()), nil
				case errors.Is(err, keychain.ErrBadPayload):
					return marshalErr(rpcBadPayload, keychain.ErrBadPayload.Error()), nil

				default:
					return marshalErr(30, "sign: "+err.Error()), nil
				}
			}

			l.Debug("SIGNED", "tz4", tz4)

			result, err := proto.Marshal(&signer.Response{
				Payload: &signer.Response_Sign{
					Sign: &signer.SignResponse{Signature: sig},
				},
			})

			return result, err

		case *signer.Request_NewKeys:
			pass := p.NewKeys.GetPassphrase()
			defer keychain.MemoryWipe(pass)
			ids := p.NewKeys.GetKeyIds()
			if len(ids) == 0 {
				ids = []string{""}
			}

			results := make([]*signer.NewKeyPerKeyResult, 0, len(ids))
			for _, alias := range ids {
				id, blPubkey, tz4, err := kr.CreateKey(alias, pass)
				r := &signer.NewKeyPerKeyResult{
					KeyId:    id,
					BlPubkey: blPubkey,
					Tz4:      tz4,
				}
				if err != nil {
					if id == "" {
						r.KeyId = alias
					}
					r.Ok = false
					r.Error = err.Error()
					l.Error("NEW_KEY", "alias", alias, "err", err)
				} else {
					r.Ok = true
					l.Debug("NEW_KEY", "key", id, "tz4", tz4)
				}
				results = append(results, r)
			}

			l.Debug("NEW_KEY batch", "count", len(ids))

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_NewKey{
					NewKey: &signer.NewKeysResponse{
						Results: results,
					},
				},
			})

		case *signer.Request_DeleteKeys:
			pass := p.DeleteKeys.GetPassphrase()
			defer keychain.MemoryWipe(pass)
			ids := p.DeleteKeys.GetKeyIds()
			if len(ids) == 0 {
				return marshalErr(90, "delete_keys: no key_ids provided"), nil
			}
			if len(pass) == 0 {
				return marshalErr(91, "delete_keys: passphrase required"), nil
			}
			if ok, wait := securedRPCLimiter.Allow(); !ok {
				l.Warn("delete_keys throttled", slog.Duration("retry_in", wait))
				msg := fmt.Sprintf(
					"delete_keys throttled: retry in ~%s (max %d attempts per %s)",
					wait.Round(time.Second),
					securedAttemptLimit,
					securedAttemptWindow,
				)
				return marshalErr(rpcDeleteThrottled, msg), nil
			}
			if err := kr.VerifyMasterPassword(pass); err != nil {
				l.Warn("delete_keys: bad passphrase", slog.Any("err", err))
				return marshalErr(rpcDeleteBadPass, "delete_keys: invalid passphrase"), nil
			}

			results := make([]*signer.PerKeyResult, 0, len(ids))
			for _, alias := range ids {
				res := &signer.PerKeyResult{KeyId: alias}
				if err := kr.DeleteKey(alias); err != nil {
					res.Ok = false
					res.Error = err.Error()
					l.Error("DELETE_KEY", "key", alias, "err", err)
				} else {
					res.Ok = true
					l.Debug("DELETE_KEY", "key", alias)
				}
				results = append(results, res)
			}

			l.Debug("DELETE_KEY batch", "count", len(ids))

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_DeleteKeys{
					DeleteKeys: &signer.DeleteKeysResponse{
						Results: results,
					},
				},
			})

		case *signer.Request_Logs:
			path := logging.CurrentFile()
			if path == "" {
				return marshalErr(50, "logs: file logging not enabled"), nil
			}

			lim := int(p.Logs.GetLimit())
			lines, err := logging.TailLastLines(path, lim)
			if err != nil {
				return marshalErr(51, fmt.Sprintf("logs: %v", err)), nil
			}

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_Logs{
					Logs: &signer.LogsResponse{Lines: lines},
				},
			})

		case *signer.Request_InitMaster:
			det := p.InitMaster.GetDeterministic()
			pass := p.InitMaster.GetPassphrase()
			defer keychain.MemoryWipe(pass)

			if len(pass) == 0 {
				return marshalErr(60, "init_master: passphrase required"), nil
			}

			// master.json
			if err := fs.InitMaster(); err != nil {
				return marshalErr(61, "init_master: "+err.Error()), nil
			}

			// seed.bin under KEK(pass)
			if err := fs.WriteSeed(pass, det); err != nil {
				return marshalErr(62, "init_master/seed: "+err.Error()), nil
			}

			return marshalOK(true), nil

		case *signer.Request_InitInfo:
			master, det, e := fs.InitInfo()
			if e != nil {
				return marshalErr(70, "init_info: "+e.Error()), nil
			}

			return proto.Marshal(&signer.Response{
				Payload: &signer.Response_InitInfo{
					InitInfo: &signer.InitInfoResponse{
						MasterPresent:        master,
						DeterministicEnabled: det,
					},
				},
			})

		case *signer.Request_SetLevel:
			keyID := p.SetLevel.GetKeyId()
			if err := kr.SetLevel(keyID, p.SetLevel.GetLevel()); err != nil {
				return marshalErr(80, fmt.Sprintf("set_level for key=%s error: %v", keyID, err)), nil
			}

			return marshalOK(true), nil

		default:
			return marshalErr(1000, "unknown request"), nil
		}
	}

	bLogger := broker.WithLogger(l.With("component", "broker"))

	// IF0: sign channel
	signBroker := broker.New(r0, w0, bLogger, broker.WithHandler(handleSignAndStatus(handleAll)))
	// IF1: management channel
	mgmtBroker := broker.New(r1, w1, bLogger, broker.WithHandler(handleMgmtOnly(handleAll)))

	cleanupSock := serveReadySocket(l)

	l.Info("Signer gadget online; awaiting requests.")

	// Graceful shutdown on SIGTERM/SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigCh
	l.Info("Received shutdown signal, cleaning up...", slog.String("signal", sig.String()))

	// Clean up in reverse order of creation
	cleanupSock()
	mgmtBroker.Stop()
	signBroker.Stop()

	// Close readers/writers
	r0.Close()
	w0.Close()
	r1.Close()
	w1.Close()

	l.Info("Gadget shutdown complete")
	return nil
}
