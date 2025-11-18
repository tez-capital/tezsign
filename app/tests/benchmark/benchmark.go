package main

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/samber/lo"
	"github.com/tez-capital/tezsign/broker"
	"github.com/tez-capital/tezsign/common"
	"github.com/tez-capital/tezsign/logging"
)

func main() {
	logCfg := logging.NewConfigFromEnv()
	if logCfg.File == "" {
		logCfg.File = logging.DefaultFileInExecDir("host.log")
	}

	if err := logging.EnsureDir(logCfg.File); err != nil {
		panic("Could not create dir for path of configuration file!")
	}

	l, _ := logging.New(logCfg)

	l.Info("logging to file", "path", logging.CurrentFile())

	// connect to gadget (USB FFS + broker inside common)
	session, err := common.Connect(common.ConnectParams{Logger: l})
	defer session.Close()
	if err != nil {
		l.Error("connect", slog.Any("err", err))
		return
	}

	b := session.Broker
	l.Info("connected", slog.String("serial", session.Serial))

	// ---- MOCK FLOW ----
	masterPass := []byte("pass")

	// 0) (Optional) init master once — if already initialized, treat as OK.
	if ok, err := common.ReqInitMaster(b, false, masterPass); err != nil {
		l.Warn("init master", slog.Any("err", err))
	} else if ok {
		l.Info("master initialized", slog.Bool("deterministic", false))
	}

	// 1) status (cold boot — probably empty or no last-levels)
	status, _ := common.ReqStatus(b)
	l.Info("status (boot)", slog.Any("status", status))

	// 2) create keys (with passphrase) if it doesn’t exist yet
	keys, err := common.ReqNewKeys(b, []string{""}, masterPass)
	keyID := keys[0].GetKeyId()

	if err == nil {
		l.Info("new key", slog.String("id", keyID), slog.String("tz4", keys[0].GetTz4()))
	} else {
		// In the future, if it already exists, continue; only error out on non-exists errors
		l.Warn("new key failed", slog.Any("err", err))
	}

	// 3) unlock key
	rs, err := common.ReqUnlockKeys(b, []string{keyID}, masterPass)
	if err != nil {
		l.Error("unlock", slog.Any("err", err))
		return
	}
	l.Info("unlocked", slog.String("key", keyID), slog.Any("result", rs[0]))

	// 4) benchmark on the new key
	benchmarkRoundtrip(b, l, keyID)

	// 5) show status again
	status2, _ := common.ReqStatus(b)
	l.Info("status (after signing)", slog.Any("status", status2))

	// 6) lock key
	rs, err = common.ReqLockKeys(b, []string{keyID})
	if err != nil {
		l.Error("lock", slog.Any("err", err))
	}
	l.Info("locked", slog.String("key", keyID), slog.Any("result", rs[0]))
}

// benchmarkRoundtrip runs N sign requests and prints min, max, avg, median latencies.
func benchmarkRoundtrip(b *broker.Broker, l *slog.Logger, key string) {
	const N = 1000
	durations := make([]time.Duration, 0, N)

	// sign same message with increasing levels so gadget accepts them
	for i := 0; i < N; i++ {
		msg := []byte(fmt.Sprintf("bench-%d", i))
		level := uint64(i + 1)

		t0 := time.Now()
		_, err := common.ReqSign(b, key, buildTenderbakePayload(0x11, level, 0, msg))
		dt := time.Since(t0)
		if err != nil {
			l.Error("roundtrip failed", slog.Int("i", i), slog.Any("err", err))
			continue
		}
		durations = append(durations, dt)
	}

	if len(durations) == 0 {
		l.Warn("benchmark: no successful samples")
		return
	}

	// sort for median
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	// stats
	min := lo.Min(durations)
	max := lo.Max(durations)

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	avg := sum / time.Duration(len(durations))

	median := durations[len(durations)/2] // N is even => lower median

	l.Info("Roundtrip benchmark",
		slog.Int("samples", len(durations)),
		slog.String("min", min.String()),
		slog.String("max", max.String()),
		slog.String("avg", avg.String()),
		slog.String("median", median.String()),
	)
}
