package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/logging"
	"github.com/tez-capital/tezsign/secure"
	"golang.org/x/term"
)

type benchmarkConfig struct {
	samples          int
	warmup           int
	kind             string
	pass             string
	device           string
	keyID            string
	keyID2           string
	mode             string
	realLifePairs    int
	realLifeInterval time.Duration
	cleanup          bool
}

type benchmarkTarget struct {
	name string
	kind byte
}

type benchmarkResult struct {
	target    benchmarkTarget
	successes int
	failures  int
	total     time.Duration
	min       time.Duration
	max       time.Duration
	avg       time.Duration
	p50       time.Duration
	p95       time.Duration
	p99       time.Duration
	opsPerSec float64
}

type benchmarkKey struct {
	id      string
	tz4     string
	created bool
}

type brokerGetter func() *broker.Broker
type reconnectFn func(cause error) error
type watermarkReaderFn func() (level uint64, round uint32, err error)

type signAttemptState struct {
	keyID                    string
	attemptLevel             uint64
	attemptRound             uint32
	benchmarkLastSignedLevel uint64
	benchmarkLastSignedRound uint32
	readStorageHWM           watermarkReaderFn
}

const (
	benchmarkReconnectMaxAttempts = 60
	benchmarkReconnectRetryDelay  = 500 * time.Millisecond
)

func main() {
	cfg := parseConfig()

	logCfg := logging.NewConfigFromEnv()
	if logCfg.File == "" {
		logCfg.File = logging.DefaultFileInExecDir("host.log")
	}

	if err := logging.EnsureDir(logCfg.File); err != nil {
		panic("Could not create dir for path of configuration file!")
	}

	l, _ := logging.New(logCfg)
	l.Info("logging to file", "path", logging.CurrentFile())
	l.Info("benchmark config",
		"samples", cfg.samples,
		"warmup", cfg.warmup,
		"kind", cfg.kind,
		"device", cfg.device,
		"key_id", cfg.keyID,
		"key_id_2", cfg.keyID2,
		"mode", cfg.mode,
		"real_life_pairs", cfg.realLifePairs,
		"real_life_interval", cfg.realLifeInterval,
		"cleanup", cfg.cleanup,
	)

	mgmtSession, signSession, err := connectBenchmarkSessions(l, cfg.device)
	if err != nil {
		l.Error("connect", slog.Any("err", err))
		os.Exit(1)
	}

	serial := mgmtSession.Serial
	mgmtBroker := mgmtSession.Broker
	signBroker := signSession.Broker
	l.Info("connected", slog.String("serial", serial))

	closeSessions := func() {
		if signSession != nil {
			signSession.Close()
			signSession = nil
		}
		if mgmtSession != nil {
			mgmtSession.Close()
			mgmtSession = nil
		}
	}
	defer closeSessions()

	getMgmtBroker := func() *broker.Broker { return mgmtBroker }
	getSignBroker := func() *broker.Broker { return signBroker }

	reconnectSessions := func(cause error) error {
		l.Warn("benchmark transport error; reconnecting",
			slog.String("serial", serial),
			slog.Any("cause", cause),
		)
		closeSessions()

		var lastErr error
		for attempt := 1; attempt <= benchmarkReconnectMaxAttempts; attempt++ {
			nextMgmt, nextSign, err := connectBenchmarkSessions(l, serial)
			if err == nil {
				mgmtSession = nextMgmt
				signSession = nextSign
				mgmtBroker = mgmtSession.Broker
				signBroker = signSession.Broker
				l.Info("benchmark reconnected",
					slog.String("serial", serial),
					slog.Int("attempt", attempt),
				)
				return nil
			}

			lastErr = err
			l.Warn("benchmark reconnect attempt failed",
				slog.Int("attempt", attempt),
				slog.Any("err", err),
			)
			time.Sleep(benchmarkReconnectRetryDelay)
		}

		if lastErr == nil {
			lastErr = fmt.Errorf("unknown reconnect error")
		}
		return fmt.Errorf("reconnect failed after %d attempts: %w", benchmarkReconnectMaxAttempts, lastErr)
	}

	masterPass, err := resolveMasterPass(cfg)
	if err != nil {
		l.Error("resolve master passphrase", slog.Any("err", err))
		os.Exit(1)
	}
	defer secure.MemoryWipe(masterPass)

	if info, err := common.ReqInitInfo(getMgmtBroker()); err != nil {
		l.Warn("init info", slog.Any("err", err))
	} else if !info.GetMasterPresent() {
		ok, err := common.ReqInitMaster(getMgmtBroker(), false, masterPass)
		if err != nil {
			l.Error("init master", slog.Any("err", err))
			os.Exit(1)
		}
		if ok {
			l.Info("master initialized", slog.Bool("deterministic", false))
		}
	} else {
		l.Info("master already initialized", slog.Bool("deterministic", info.GetDeterministicEnabled()))
	}

	switch normalizeBenchmarkMode(cfg.mode) {
	case "latency":
		if err := runLatencyMode(getMgmtBroker, getSignBroker, reconnectSessions, masterPass, cfg, l); err != nil {
			l.Error("benchmark failed", slog.Any("err", err))
			os.Exit(1)
		}
	case "real-life":
		if err := runRealLifeMode(getMgmtBroker, getSignBroker, reconnectSessions, masterPass, cfg, l); err != nil {
			l.Error("real-life benchmark failed", slog.Any("err", err))
			os.Exit(1)
		}
	default:
		l.Error("benchmark config", slog.String("err", fmt.Sprintf("unknown mode %q", cfg.mode)))
		os.Exit(1)
	}
}

func runLatencyMode(
	getMgmtBroker brokerGetter,
	getSignBroker brokerGetter,
	reconnectSessions reconnectFn,
	masterPass []byte,
	cfg benchmarkConfig,
	l *slog.Logger,
) error {
	keyID, keyTz4, created, err := resolveBenchmarkKey(getMgmtBroker(), masterPass, cfg, l)
	if err != nil {
		return fmt.Errorf("resolve benchmark key: %w", err)
	}
	key := benchmarkKey{id: keyID, tz4: keyTz4, created: created}

	unlocked := false
	defer func() {
		if !unlocked {
			return
		}

		cleanupBenchmarkKeys(getMgmtBroker(), []benchmarkKey{key}, masterPass, cfg.cleanup, l)
	}()

	if err := unlockBenchmarkKeys(getMgmtBroker(), []benchmarkKey{key}, masterPass, cfg); err != nil {
		return err
	}
	unlocked = true

	reconnectAndUnlock := func(cause error) error {
		if reconnectSessions == nil {
			return fmt.Errorf("reconnect unavailable: %w", cause)
		}
		if err := reconnectSessions(cause); err != nil {
			return err
		}
		if err := unlockBenchmarkKeys(getMgmtBroker(), []benchmarkKey{key}, masterPass, cfg); err != nil {
			return fmt.Errorf("unlock after reconnect: %w", err)
		}
		return nil
	}

	targets, err := resolveTargets(cfg.kind)
	if err != nil {
		return fmt.Errorf("benchmark config: %w", err)
	}

	fmt.Println("== End-to-End")
	fmt.Printf("benchmark key=%s tz4=%s samples=%d warmup=%d\n", keyID, keyTz4, cfg.samples, cfg.warmup)
	for _, target := range targets {
		lastLevel, lastRound, err := benchmarkKindWatermarkFromStatus(getMgmtBroker(), keyID, target.kind)
		if err != nil {
			return fmt.Errorf("%s: read current watermark: %w", target.name, err)
		}
		startLevel := lastLevel + 1
		if startLevel == 0 {
			return fmt.Errorf("%s: cannot continue benchmark: level overflow", target.name)
		}

		l.Info("benchmark start watermark",
			"kind", target.name,
			"key", keyID,
			"last_level", lastLevel,
			"last_round", lastRound,
			"start_level", startLevel,
		)

		readStorageHWM := func() (uint64, uint32, error) {
			return benchmarkKindWatermarkFromStatus(getMgmtBroker(), keyID, target.kind)
		}
		result, err := runBenchmark(getSignBroker, keyID, keyTz4, target, startLevel, cfg.samples, cfg.warmup, readStorageHWM, reconnectAndUnlock, l)
		if err != nil {
			return fmt.Errorf("%s: %w", target.name, err)
		}
		printBenchmarkResult(result)
	}

	fmt.Println()
	fmt.Println("== Local Keychain Comparison")
	if err := runLocalKeychainComparison(cfg, targets); err != nil {
		return fmt.Errorf("local keychain comparison: %w", err)
	}

	return nil
}

func parseConfig() benchmarkConfig {
	var cfg benchmarkConfig

	flag.IntVar(&cfg.samples, "n", 1000, "number of measured sign requests per kind")
	flag.IntVar(&cfg.warmup, "warmup", 50, "number of warmup sign requests per kind")
	flag.StringVar(&cfg.kind, "kind", "all", "kind to benchmark: block, preattestation, attestation, or all")
	flag.StringVar(&cfg.pass, "pass", "", "master password used for init/unlock; falls back to TEZSIGN_BENCH_PASS or an interactive prompt")
	flag.StringVar(&cfg.device, "device", "", "target device serial; if empty, auto-select the first available device")
	flag.StringVar(&cfg.keyID, "key", "", "existing key id to reuse; if empty a fresh benchmark key is created")
	flag.StringVar(&cfg.keyID2, "key2", "", "second existing key id for -mode real-life; if empty a fresh key is created")
	flag.StringVar(&cfg.mode, "mode", "latency", "benchmark mode: latency or real-life")
	flag.IntVar(&cfg.realLifePairs, "real-life-pairs", 10, "number of cycles for -mode real-life; each cycle signs once with the same two keys")
	flag.DurationVar(&cfg.realLifeInterval, "real-life-interval", 3*time.Second, "sleep between generated payloads for -mode real-life")
	flag.BoolVar(&cfg.cleanup, "cleanup", true, "delete the auto-created benchmark key after the run")
	flag.Parse()

	return cfg
}

func normalizeBenchmarkMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "latency", "end-to-end", "e2e":
		return "latency"
	case "real-life", "reallife", "real":
		return "real-life"
	default:
		return mode
	}
}

func connectBenchmarkSessions(l *slog.Logger, serial string) (*common.Session, *common.Session, error) {
	mgmtSession, err := common.Connect(common.ConnectParams{
		Logger:  l,
		Channel: common.ChanMgmt,
		Serial:  serial,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("connect mgmt: %w", err)
	}

	signSerial := serial
	if signSerial == "" {
		signSerial = mgmtSession.Serial
	}
	signSession, err := common.Connect(common.ConnectParams{
		Logger:  l,
		Channel: common.ChanSign,
		Serial:  signSerial,
	})
	if err != nil {
		mgmtSession.Close()
		return nil, nil, fmt.Errorf("connect sign: %w", err)
	}

	return mgmtSession, signSession, nil
}

func resolveMasterPass(cfg benchmarkConfig) ([]byte, error) {
	if cfg.pass != "" {
		return []byte(cfg.pass), nil
	}

	if envPass := strings.TrimSpace(os.Getenv("TEZSIGN_BENCH_PASS")); envPass != "" {
		return []byte(envPass), nil
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Master passphrase: ")
		pass, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		if len(pass) == 0 {
			return nil, fmt.Errorf("empty passphrase")
		}
		return pass, nil
	}

	return nil, fmt.Errorf("master passphrase required; use -pass, TEZSIGN_BENCH_PASS, or run from a TTY")
}

func resolveTargets(kind string) ([]benchmarkTarget, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "all":
		return []benchmarkTarget{
			{name: "block", kind: 0x11},
			{name: "preattestation", kind: 0x12},
			{name: "attestation", kind: 0x13},
		}, nil
	case "block":
		return []benchmarkTarget{{name: "block", kind: 0x11}}, nil
	case "preattestation", "pre":
		return []benchmarkTarget{{name: "preattestation", kind: 0x12}}, nil
	case "attestation", "att":
		return []benchmarkTarget{{name: "attestation", kind: 0x13}}, nil
	default:
		return nil, fmt.Errorf("unknown benchmark kind %q", kind)
	}
}

func resolveBenchmarkKey(mgmtBroker *broker.Broker, masterPass []byte, cfg benchmarkConfig, l *slog.Logger) (string, string, bool, error) {
	key, err := resolveBenchmarkKeyByID(mgmtBroker, masterPass, cfg.keyID, "bench", l)
	if err != nil {
		return "", "", false, err
	}
	return key.id, key.tz4, key.created, nil
}

func resolveBenchmarkKeyByID(mgmtBroker *broker.Broker, masterPass []byte, requestedKeyID, prefix string, l *slog.Logger) (benchmarkKey, error) {
	if requestedKeyID != "" {
		status, err := common.ReqStatus(mgmtBroker)
		if err != nil {
			return benchmarkKey{}, err
		}
		for _, key := range status.GetKeys() {
			if key.GetKeyId() == requestedKeyID {
				return benchmarkKey{id: key.GetKeyId(), tz4: key.GetTz4()}, nil
			}
		}
		return benchmarkKey{}, fmt.Errorf("key %q not found", requestedKeyID)
	}

	keyID := fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	results, err := common.ReqNewKeys(mgmtBroker, []string{keyID}, masterPass)
	if err != nil {
		return benchmarkKey{}, err
	}
	if len(results) == 0 {
		return benchmarkKey{}, fmt.Errorf("new key returned no result")
	}
	if !results[0].GetOk() {
		return benchmarkKey{}, fmt.Errorf("new key failed: %s", results[0].GetError())
	}

	l.Info("created benchmark key",
		"key", results[0].GetKeyId(),
		"tz4", results[0].GetTz4(),
	)

	return benchmarkKey{id: results[0].GetKeyId(), tz4: results[0].GetTz4(), created: true}, nil
}

func unlockBenchmarkKeys(mgmtBroker *broker.Broker, keys []benchmarkKey, masterPass []byte, cfg benchmarkConfig) error {
	keyIDs := benchmarkKeyIDs(keys)
	results, err := common.ReqUnlockKeys(mgmtBroker, keyIDs, masterPass)
	if err != nil {
		return fmt.Errorf("unlock: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("unlock: empty response")
	}

	for i, result := range results {
		keyID := fmt.Sprintf("index %d", i)
		if i < len(keyIDs) {
			keyID = keyIDs[i]
		}
		if result.GetOk() {
			continue
		}

		errMsg := result.GetError()
		if cfg.keyID != "" || cfg.keyID2 != "" {
			errMsg += " (check -pass or TEZSIGN_BENCH_PASS for existing keys)"
		}
		return fmt.Errorf("unlock key %q: %s", keyID, errMsg)
	}

	return nil
}

func cleanupBenchmarkKeys(mgmtBroker *broker.Broker, keys []benchmarkKey, masterPass []byte, cleanup bool, l *slog.Logger) {
	keyIDs := benchmarkKeyIDs(keys)
	if len(keyIDs) > 0 {
		results, err := common.ReqLockKeys(mgmtBroker, keyIDs)
		if err != nil {
			l.Warn("lock benchmark keys", slog.Any("err", err))
		} else {
			for i, result := range results {
				if result.GetOk() {
					continue
				}
				keyID := fmt.Sprintf("index %d", i)
				if i < len(keyIDs) {
					keyID = keyIDs[i]
				}
				l.Warn("lock benchmark key", slog.String("key", keyID), slog.String("err", result.GetError()))
			}
		}
	}

	if !cleanup {
		return
	}

	createdIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.created {
			createdIDs = append(createdIDs, key.id)
		}
	}
	if len(createdIDs) == 0 {
		return
	}

	results, err := common.ReqDeleteKeys(mgmtBroker, createdIDs, masterPass)
	if err != nil {
		l.Warn("delete benchmark keys", slog.Any("err", err))
		return
	}
	for i, result := range results {
		if result.GetOk() {
			continue
		}
		keyID := fmt.Sprintf("index %d", i)
		if i < len(createdIDs) {
			keyID = createdIDs[i]
		}
		l.Warn("delete benchmark key", slog.String("key", keyID), slog.String("err", result.GetError()))
	}
}

func benchmarkKeyIDs(keys []benchmarkKey) []string {
	keyIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		keyIDs = append(keyIDs, key.id)
	}
	return keyIDs
}

func benchmarkKindWatermarkFromStatus(mgmtBroker *broker.Broker, keyID string, kind byte) (level uint64, round uint32, err error) {
	status, err := common.ReqStatus(mgmtBroker)
	if err != nil {
		return 0, 0, err
	}

	for _, key := range status.GetKeys() {
		if key.GetKeyId() != keyID {
			continue
		}

		switch kind {
		case 0x11:
			return key.GetLastBlockLevel(), key.GetLastBlockRound(), nil
		case 0x12:
			return key.GetLastPreattestationLevel(), key.GetLastPreattestationRound(), nil
		case 0x13:
			return key.GetLastAttestationLevel(), key.GetLastAttestationRound(), nil
		default:
			return 0, 0, fmt.Errorf("unknown benchmark kind 0x%02x", kind)
		}
	}

	return 0, 0, fmt.Errorf("status: key %q not found", keyID)
}

func runBenchmark(
	getSignBroker brokerGetter,
	keyID string,
	tz4 string,
	target benchmarkTarget,
	startLevel uint64,
	samples, warmup int,
	readStorageHWM watermarkReaderFn,
	reconnect reconnectFn,
	l *slog.Logger,
) (benchmarkResult, error) {
	if samples <= 0 {
		return benchmarkResult{}, fmt.Errorf("samples must be > 0")
	}
	if warmup < 0 {
		return benchmarkResult{}, fmt.Errorf("warmup must be >= 0")
	}
	if startLevel == 0 {
		return benchmarkResult{}, fmt.Errorf("start level must be > 0")
	}

	nextLevel := startLevel
	lastSignedLevel := startLevel - 1
	lastSignedRound := uint32(0)
	for i := 0; i < warmup; i++ {
		level := nextLevel
		round := uint32(0)
		payload := buildTenderbakePayload(target.kind, level, round, nil)
		attempt := signAttemptState{
			keyID:                    keyID,
			attemptLevel:             level,
			attemptRound:             round,
			benchmarkLastSignedLevel: lastSignedLevel,
			benchmarkLastSignedRound: lastSignedRound,
			readStorageHWM:           readStorageHWM,
		}
		if _, _, err := signWithReconnect(getSignBroker, reconnect, tz4, payload, l, target.name, "warmup", i, attempt); err != nil {
			return benchmarkResult{}, fmt.Errorf("warmup %s[%d]: %w", target.name, i, err)
		}
		lastSignedLevel = level
		lastSignedRound = round
		nextLevel++
	}

	type samplePayload struct {
		raw   []byte
		level uint64
		round uint32
	}

	payloads := make([]samplePayload, 0, samples)
	for i := 0; i < samples; i++ {
		level := nextLevel
		round := uint32(0)
		payloads = append(payloads, samplePayload{
			raw:   buildTenderbakePayload(target.kind, level, round, nil),
			level: level,
			round: round,
		})
		nextLevel++
	}

	durations := make([]time.Duration, 0, samples)
	failures := 0
	benchStart := time.Now()

	for i := 0; i < samples; i++ {
		sample := payloads[i]
		attempt := signAttemptState{
			keyID:                    keyID,
			attemptLevel:             sample.level,
			attemptRound:             sample.round,
			benchmarkLastSignedLevel: lastSignedLevel,
			benchmarkLastSignedRound: lastSignedRound,
			readStorageHWM:           readStorageHWM,
		}
		dt, recovered, err := signWithReconnect(getSignBroker, reconnect, tz4, sample.raw, l, target.name, "sample", i, attempt)

		if err != nil {
			failures++
			l.Error("roundtrip failed",
				slog.String("kind", target.name),
				slog.Int("i", i),
				slog.Uint64("level", sample.level),
				slog.Int("round", int(sample.round)),
				slog.Uint64("benchmark_last_signed_level", lastSignedLevel),
				slog.Int("benchmark_last_signed_round", int(lastSignedRound)),
				slog.Any("err", err),
			)
			continue
		}
		if recovered {
			l.Info("roundtrip recovered after reconnect",
				slog.String("kind", target.name),
				slog.Int("i", i),
				slog.Uint64("level", sample.level),
				slog.Int("round", int(sample.round)),
				slog.Uint64("benchmark_last_signed_level_before_disconnect", lastSignedLevel),
				slog.Int("benchmark_last_signed_round_before_disconnect", int(lastSignedRound)),
			)
		}

		durations = append(durations, dt)
		lastSignedLevel = sample.level
		lastSignedRound = sample.round
	}

	if len(durations) == 0 {
		return benchmarkResult{}, fmt.Errorf("no successful samples for %s", target.name)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	total := time.Since(benchStart)
	successes := len(durations)

	return benchmarkResult{
		target:    target,
		successes: successes,
		failures:  failures,
		total:     total,
		min:       durations[0],
		max:       durations[len(durations)-1],
		avg:       sum / time.Duration(successes),
		p50:       percentileDuration(durations, 50),
		p95:       percentileDuration(durations, 95),
		p99:       percentileDuration(durations, 99),
		opsPerSec: float64(successes) / total.Seconds(),
	}, nil
}

func signWithReconnect(
	getSignBroker brokerGetter,
	reconnect reconnectFn,
	tz4 string,
	payload []byte,
	l *slog.Logger,
	kind string,
	phase string,
	index int,
	attempt signAttemptState,
) (duration time.Duration, recovered bool, err error) {
	t0 := time.Now()
	_, err = common.ReqSign(getSignBroker(), tz4, payload)
	if err == nil {
		return time.Since(t0), false, nil
	}
	if reconnect == nil || !isReconnectableSignError(err) {
		return time.Since(t0), false, err
	}

	l.Warn("sign request failed with transport error; reconnecting",
		slog.String("kind", kind),
		slog.String("phase", phase),
		slog.Int("index", index),
		slog.String("key", attempt.keyID),
		slog.Uint64("inflight_level", attempt.attemptLevel),
		slog.Int("inflight_round", int(attempt.attemptRound)),
		slog.Uint64("benchmark_last_signed_level", attempt.benchmarkLastSignedLevel),
		slog.Int("benchmark_last_signed_round", int(attempt.benchmarkLastSignedRound)),
		slog.String("benchmark_last_signed_meaning", "last successful sign response before disconnect"),
		slog.Any("err", err),
	)
	logDisconnectSnapshot(l, kind, attempt, "before_reconnect")
	if recErr := reconnect(err); recErr != nil {
		return time.Since(t0), false, fmt.Errorf("reconnect failed: %w", recErr)
	}
	logDisconnectSnapshot(l, kind, attempt, "after_reconnect")
	if err := verifyPersistedLastSignedHWM(l, kind, phase, index, attempt); err != nil {
		return time.Since(t0), true, err
	}
	l.Info("-------------------------")
	l.Info("HWM PERSISTED ON RECONNECT",
		slog.String("kind", kind),
		slog.String("phase", phase),
		slog.Int("index", index),
		slog.String("key", attempt.keyID),
		slog.Uint64("benchmark_last_signed_level", attempt.benchmarkLastSignedLevel),
		slog.Int("benchmark_last_signed_round", int(attempt.benchmarkLastSignedRound)),
	)
	l.Info("-------------------------")

	t1 := time.Now()
	_, err = common.ReqSign(getSignBroker(), tz4, payload)
	if err != nil {
		return time.Since(t1), true, err
	}
	return time.Since(t1), true, nil
}

func logDisconnectSnapshot(l *slog.Logger, kind string, attempt signAttemptState, stage string) {
	if attempt.readStorageHWM == nil || attempt.keyID == "" {
		return
	}

	storageLevel, storageRound, err := attempt.readStorageHWM()
	if err != nil {
		l.Warn("disconnect state snapshot unavailable",
			slog.String("stage", stage),
			slog.String("kind", kind),
			slog.String("key", attempt.keyID),
			slog.Any("err", err),
		)
		return
	}

	l.Warn("disconnect state snapshot",
		slog.String("stage", stage),
		slog.String("kind", kind),
		slog.String("key", attempt.keyID),
		slog.Uint64("inflight_level", attempt.attemptLevel),
		slog.Int("inflight_round", int(attempt.attemptRound)),
		slog.Uint64("benchmark_last_signed_level", attempt.benchmarkLastSignedLevel),
		slog.Int("benchmark_last_signed_round", int(attempt.benchmarkLastSignedRound)),
		slog.Uint64("storage_hwm_level", storageLevel),
		slog.Int("storage_hwm_round", int(storageRound)),
	)
}

func verifyPersistedLastSignedHWM(
	l *slog.Logger,
	kind string,
	phase string,
	index int,
	attempt signAttemptState,
) error {
	if attempt.readStorageHWM == nil || attempt.keyID == "" || attempt.benchmarkLastSignedLevel == 0 {
		return nil
	}

	storageLevel, storageRound, err := attempt.readStorageHWM()
	if err != nil {
		return fmt.Errorf("read storage hwm after reconnect: %w", err)
	}

	if watermarkLess(storageLevel, storageRound, attempt.benchmarkLastSignedLevel, attempt.benchmarkLastSignedRound) {
		l.Error("persisted hwm is behind last successful sign after reconnect",
			slog.String("kind", kind),
			slog.String("phase", phase),
			slog.Int("index", index),
			slog.String("key", attempt.keyID),
			slog.Uint64("benchmark_last_signed_level", attempt.benchmarkLastSignedLevel),
			slog.Int("benchmark_last_signed_round", int(attempt.benchmarkLastSignedRound)),
			slog.Uint64("storage_hwm_level", storageLevel),
			slog.Int("storage_hwm_round", int(storageRound)),
		)
		return fmt.Errorf(
			"persisted hwm regressed after reconnect: storage (%d,%d) < benchmark_last_signed (%d,%d)",
			storageLevel,
			storageRound,
			attempt.benchmarkLastSignedLevel,
			attempt.benchmarkLastSignedRound,
		)
	}

	return nil
}

func watermarkLess(levelA uint64, roundA uint32, levelB uint64, roundB uint32) bool {
	if levelA != levelB {
		return levelA < levelB
	}
	return roundA < roundB
}

func isReconnectableSignError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "libusb") ||
		strings.Contains(msg, "no device") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset")
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 100 {
		return sorted[len(sorted)-1]
	}

	index := int(math.Ceil((float64(percentile) / 100.0) * float64(len(sorted))))
	if index <= 0 {
		index = 1
	}
	return sorted[index-1]
}

func printBenchmarkResult(result benchmarkResult) {
	fmt.Printf(
		"%s: ok=%d fail=%d total=%s ops/s=%.1f min=%s avg=%s p50=%s p95=%s p99=%s max=%s\n",
		result.target.name,
		result.successes,
		result.failures,
		result.total.Round(time.Millisecond),
		result.opsPerSec,
		result.min,
		result.avg,
		result.p50,
		result.p95,
		result.p99,
		result.max,
	)
}

func runLocalKeychainComparison(cfg benchmarkConfig, targets []benchmarkTarget) error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	benchRegex := buildLocalBenchRegex(targets)
	benchtime := fmt.Sprintf("%dx", cfg.samples)
	cmd := exec.Command(
		"go", "test", "./keychain",
		"-run", "^$",
		"-bench", benchRegex,
		"-benchmem",
		"-count=1",
		"-benchtime", benchtime,
	)
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("running: (cd %s && %s)\n", repoRoot, strings.Join(cmd.Args, " "))
	return cmd.Run()
}

func buildLocalBenchRegex(targets []benchmarkTarget) string {
	patterns := []string{"BenchmarkStatePersist"}
	for _, target := range targets {
		patterns = append(patterns, "BenchmarkSignAndPersistLocal/"+target.name)
	}
	return strings.Join(patterns, "|")
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %q", dir)
		}
		dir = parent
	}
}
