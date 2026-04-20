package main

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

type realLifeResult struct {
	target    benchmarkTarget
	pairs     int
	successes int
	failures  int
	total     time.Duration
	signs     durationStats
	pairsStat durationStats
}

type durationStats struct {
	min time.Duration
	max time.Duration
	avg time.Duration
	p50 time.Duration
	p95 time.Duration
	p99 time.Duration
}

func runRealLifeMode(
	getMgmtBroker brokerGetter,
	getSignBroker brokerGetter,
	reconnectSessions reconnectFn,
	masterPass []byte,
	cfg benchmarkConfig,
	l *slog.Logger,
) error {
	target, err := resolveRealLifeTarget(cfg.kind)
	if err != nil {
		return fmt.Errorf("benchmark config: %w", err)
	}
	if cfg.realLifePairs <= 0 {
		return fmt.Errorf("real-life pairs must be > 0")
	}
	if cfg.realLifeInterval < 0 {
		return fmt.Errorf("real-life interval must be >= 0")
	}

	key1, err := resolveBenchmarkKeyByID(getMgmtBroker(), masterPass, cfg.keyID, "bench-real-life-a", l)
	if err != nil {
		return fmt.Errorf("resolve real-life key1: %w", err)
	}
	key2, err := resolveBenchmarkKeyByID(getMgmtBroker(), masterPass, cfg.keyID2, "bench-real-life-b", l)
	if err != nil {
		if key1.created && cfg.cleanup {
			cleanupBenchmarkKeys(getMgmtBroker(), []benchmarkKey{key1}, masterPass, cfg.cleanup, l)
		}
		return fmt.Errorf("resolve real-life key2: %w", err)
	}
	if key1.id == key2.id || key1.tz4 == key2.tz4 {
		return fmt.Errorf("real-life benchmark needs two different keys")
	}

	keys := []benchmarkKey{key1, key2}
	unlocked := false
	defer func() {
		if !unlocked {
			return
		}
		cleanupBenchmarkKeys(getMgmtBroker(), keys, masterPass, cfg.cleanup, l)
	}()

	if err := unlockBenchmarkKeys(getMgmtBroker(), keys, masterPass, cfg); err != nil {
		return err
	}
	unlocked = true

	fmt.Println("== Real-Life")

	key1Level, key1Round, err := benchmarkKindWatermarkFromStatus(getMgmtBroker(), key1.id, target.kind)
	if err != nil {
		return fmt.Errorf("read current watermark for key1 %q: %w", key1.id, err)
	}
	key2Level, key2Round, err := benchmarkKindWatermarkFromStatus(getMgmtBroker(), key2.id, target.kind)
	if err != nil {
		return fmt.Errorf("read current watermark for key2 %q: %w", key2.id, err)
	}

	startLevel := key1Level
	if key2Level > startLevel {
		startLevel = key2Level
	}
	startLevel++
	if startLevel == 0 {
		return fmt.Errorf("cannot continue real-life benchmark: level overflow")
	}

	l.Info("real-life start watermark",
		"kind", target.name,
		"key1", key1.id,
		"key1_last_level", key1Level,
		"key1_last_round", key1Round,
		"key2", key2.id,
		"key2_last_level", key2Level,
		"key2_last_round", key2Round,
		"start_level", startLevel,
	)

	fmt.Printf(
		"real-life key1=%s tz4=%s key2=%s tz4=%s kind=%s cycles=%d sign_requests=%d interval=%s start_level=%d\n",
		key1.id,
		key1.tz4,
		key2.id,
		key2.tz4,
		target.name,
		cfg.realLifePairs,
		cfg.realLifePairs*2,
		cfg.realLifeInterval,
		startLevel,
	)

	reconnectAndUnlock := func(cause error) error {
		if reconnectSessions == nil {
			return fmt.Errorf("reconnect unavailable: %w", cause)
		}
		if err := reconnectSessions(cause); err != nil {
			return err
		}
		if err := unlockBenchmarkKeys(getMgmtBroker(), keys, masterPass, cfg); err != nil {
			return fmt.Errorf("unlock after reconnect: %w", err)
		}
		return nil
	}

	result, err := runRealLifeBenchmark(getSignBroker, key1, key2, target, startLevel, cfg.realLifePairs, cfg.realLifeInterval, reconnectAndUnlock, l)
	if err != nil {
		return err
	}
	printRealLifeResult(result)

	return nil
}

func resolveRealLifeTarget(kind string) (benchmarkTarget, error) {
	if strings.TrimSpace(kind) == "" || strings.EqualFold(strings.TrimSpace(kind), "all") {
		return benchmarkTarget{name: "attestation", kind: 0x13}, nil
	}

	targets, err := resolveTargets(kind)
	if err != nil {
		return benchmarkTarget{}, err
	}
	if len(targets) != 1 {
		return benchmarkTarget{}, fmt.Errorf("real-life mode needs one kind; use block, preattestation, or attestation")
	}
	return targets[0], nil
}

func runRealLifeBenchmark(
	getSignBroker brokerGetter,
	key1 benchmarkKey,
	key2 benchmarkKey,
	target benchmarkTarget,
	startLevel uint64,
	pairs int,
	interval time.Duration,
	reconnect reconnectFn,
	l *slog.Logger,
) (realLifeResult, error) {
	if startLevel == 0 {
		return realLifeResult{}, fmt.Errorf("start level must be > 0")
	}

	durations := make([]time.Duration, 0, pairs*2)
	pairDurations := make([]time.Duration, 0, pairs)
	failures := 0
	level := startLevel
	benchStart := time.Now()
	runSeed := benchStart.UTC().UnixNano()

	for i := 0; i < pairs; i++ {
		payload := buildRealLifePayload(target, level, i, runSeed)
		if len(payload) == 0 {
			return realLifeResult{}, fmt.Errorf("could not build payload for %s", target.name)
		}

		pairStart := time.Now()
		for keyIndex, key := range []benchmarkKey{key1, key2} {
			dt, recovered, err := signWithReconnect(getSignBroker, reconnect, key.tz4, payload, l, target.name, "pair", i)

			if err != nil {
				failures++
				l.Error("real-life sign failed",
					slog.String("kind", target.name),
					slog.Int("pair", i),
					slog.Int("key_index", keyIndex+1),
					slog.String("key", key.id),
					slog.Uint64("level", level),
					slog.Any("err", err),
				)
				continue
			}
			if recovered {
				l.Info("real-life sign recovered after reconnect",
					slog.String("kind", target.name),
					slog.Int("pair", i),
					slog.Int("key_index", keyIndex+1),
					slog.String("key", key.id),
					slog.Uint64("level", level),
				)
			}

			durations = append(durations, dt)
			l.Info("real-life sign",
				slog.String("kind", target.name),
				slog.Int("pair", i),
				slog.Int("key_index", keyIndex+1),
				slog.String("key", key.id),
				slog.Uint64("level", level),
				slog.Duration("duration", dt),
			)
		}
		pairDurations = append(pairDurations, time.Since(pairStart))

		level++
		if i+1 < pairs && interval > 0 {
			time.Sleep(interval)
		}
	}

	if len(durations) == 0 {
		return realLifeResult{}, fmt.Errorf("no successful real-life samples for %s", target.name)
	}

	return realLifeResult{
		target:    target,
		pairs:     pairs,
		successes: len(durations),
		failures:  failures,
		total:     time.Since(benchStart),
		signs:     summarizeDurations(durations),
		pairsStat: summarizeDurations(pairDurations),
	}, nil
}

func buildRealLifePayload(target benchmarkTarget, level uint64, pair int, runSeed int64) []byte {
	payload := buildTenderbakePayload(target.kind, level, 0, nil)
	seed := []byte(fmt.Sprintf("tezsign-real-life:%d:%s:%d:%d", runSeed, target.name, level, pair))
	fillTenderbakePayloadData(target.kind, payload, seed)
	return payload
}

func fillTenderbakePayloadData(kind byte, payload, seed []byte) {
	switch kind {
	case 0x11:
		fillPayloadSegments(payload, seed, [][2]int{{1, 5}, {10, 42}, {42, 50}, {51, 83}})
	case 0x12, 0x13:
		fillPayloadSegments(payload, seed, [][2]int{{1, 37}})
	}
}

func fillPayloadSegments(payload, seed []byte, segments [][2]int) {
	if len(seed) == 0 {
		return
	}
	offset := 0
	for _, segment := range segments {
		start, end := segment[0], segment[1]
		if start < 0 || end > len(payload) || start >= end {
			continue
		}
		for i := start; i < end; i++ {
			payload[i] = seed[offset%len(seed)]
			offset++
		}
	}
}

func summarizeDurations(durations []time.Duration) durationStats {
	if len(durations) == 0 {
		return durationStats{}
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}

	return durationStats{
		min: durations[0],
		max: durations[len(durations)-1],
		avg: sum / time.Duration(len(durations)),
		p50: percentileDuration(durations, 50),
		p95: percentileDuration(durations, 95),
		p99: percentileDuration(durations, 99),
	}
}

func printRealLifeResult(result realLifeResult) {
	signsPerSec := float64(result.successes) / result.total.Seconds()
	fmt.Printf(
		"%s real-life: pairs=%d sign_ok=%d sign_fail=%d total=%s signs/s=%.2f sign_min=%s sign_avg=%s sign_p50=%s sign_p95=%s sign_p99=%s sign_max=%s pair_avg=%s pair_p95=%s pair_max=%s\n",
		result.target.name,
		result.pairs,
		result.successes,
		result.failures,
		result.total.Round(time.Millisecond),
		signsPerSec,
		result.signs.min,
		result.signs.avg,
		result.signs.p50,
		result.signs.p95,
		result.signs.p99,
		result.signs.max,
		result.pairsStat.avg,
		result.pairsStat.p95,
		result.pairsStat.max,
	)
}
