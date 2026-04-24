package keychain

import (
	"errors"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"testing"

	"github.com/tez-capital/tezsign/signer"
)

func TestSignAndUpdateRejectsCorruptedHWM(t *testing.T) {
	setup := newBenchmarkSetup(t)
	setup.key.markHWMCorrupted(true)

	_, err := setup.ring.SignAndUpdate(setup.tz4, buildPreattestationPayload(1, 0))
	if !errors.Is(err, ErrKeyStateCorrupted) {
		t.Fatalf("expected ErrKeyStateCorrupted, got %v", err)
	}
}

func TestUnlockFailureDoesNotLeaveGhostKey(t *testing.T) {
	setup := newBenchmarkSetup(t)

	err := setup.ring.Unlock("ghost-key", []byte("wrong-password"))
	if err == nil {
		t.Fatalf("expected unlock error for unknown key")
	}
	if key := setup.ring.get("ghost-key"); key != nil {
		t.Fatalf("unlock failure left a ghost key in memory")
	}

	createdID, _, _, err := setup.ring.CreateKey("ghost-key", []byte("bench-passphrase"))
	if err != nil {
		t.Fatalf("CreateKey after failed unlock: %v", err)
	}
	if createdID != "ghost-key" {
		t.Fatalf("expected created key id ghost-key, got %q", createdID)
	}
}

func TestSignAndUpdateRejectsConcurrentDuplicateLevel(t *testing.T) {
	setup := newBenchmarkSetup(t)
	payload := buildPreattestationPayload(42, 0)

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := setup.ring.SignAndUpdate(setup.tz4, payload)
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	var successCount int
	var staleCount int
	for err := range errs {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrStaleWatermark):
			staleCount++
		default:
			t.Fatalf("unexpected SignAndUpdate error: %v", err)
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly one successful signature, got %d", successCount)
	}
	if staleCount != workers-1 {
		t.Fatalf("expected %d stale watermark errors, got %d", workers-1, staleCount)
	}

	state := setup.key.GetKeyState()
	pre := state.ByKind[int32(PREATTESTATION)]
	if pre == nil {
		t.Fatalf("missing preattestation state after signing")
	}
	if pre.Level != 42 || pre.Round != 0 {
		t.Fatalf("unexpected preattestation watermark: level=%d round=%d", pre.Level, pre.Round)
	}
}

func be32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func buildPreattestationPayload(level uint64, round uint32) []byte {
	// Layout your gadget expects for tz4 (slot omitted):
	// w(1)=0x12 | chain_id(4) | branch(32) | kind(1) | level(i32) | round(i32)
	const (
		wm         = 0x12
		chainIDLen = 4
		branchLen  = 32
		kindLen    = 1
		i32        = 4
	)
	total := 1 + chainIDLen + branchLen + kindLen + i32 + i32
	buf := make([]byte, total)

	i := 0
	buf[i] = wm
	i++

	// chain_id (dummy)
	copy(buf[i:i+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	i += 4

	// branch (32) zeros
	i += 32

	// inner kind (Tezos op kind) — not used by your decoder; put 0x01
	buf[i] = 0x01
	i++

	// level, round (int32 be)
	be32(buf[i:i+4], uint32(level))
	i += 4
	be32(buf[i:i+4], round)

	return buf
}

func BenchmarkSignAndUpdate(b *testing.B) {
	// 1. Setup temporary environment
	tmpDir, _ := os.MkdirTemp("", "bench-keychain-*")
	defer os.RemoveAll(tmpDir)

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	store, _ := NewFileStore(tmpDir) // Assuming NewFileStore takes a path
	store.InitMaster()
	store.WriteSeed([]byte("test"), false)
	kr := NewKeyRing(log, store)

	secretKey, pubkeyBytes, blPubkey := signer.GenerateRandomKey()
	tz4, _ := signer.Tz4FromBLPubkeyBytes(pubkeyBytes)
	skLE := secretKey.ToLEndian()

	// 2. Create and Unlock a key for benchmarking
	password := []byte("super-secret")
	id := "bench-key"
	err := store.createKey("bench-key", password, skLE, blPubkey, tz4, "")
	// id, _, tz4, err := kr.CreateKey("bench-key", password)
	if err != nil {
		b.Fatalf("failed to create key: %v", err)
	}
	if err := kr.Unlock(id, password); err != nil {
		b.Fatalf("failed to unlock: %v", err)
	}

	// 3. Prepare a dummy payload
	// This should be a valid encoded payload that your DecodeAndValidateSignPayload accepts
	// Using a dummy block at level 100
	level, round := 0, 0

	os.OpenFile("aaa", syscall.O_DSYNC, 0666)

	b.ResetTimer()
	b.ReportAllocs() // This is the magic flag for GC tracking

	for i := 0; i < b.N; i++ {
		// We increment level each time to avoid ErrStaleWatermark
		// Note: You might need to generate a new valid 'payload' bytes
		// inside the loop if DecodeAndValidateSignPayload checks signatures.
		level += 1
		payload := buildPreattestationPayload(uint64(level), uint32(round))
		_, err := kr.SignAndUpdate(tz4, payload)
		if err != nil && err != ErrStaleWatermark {
			b.Fatal(err)
		}
		level++
	}
}
