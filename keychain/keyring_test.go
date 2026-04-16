package keychain

import (
	"log/slog"
	"os"
	"syscall"
	"testing"

	"github.com/tez-capital/tezsign/signer"
)

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
