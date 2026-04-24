package keychain

import (
	"io"
	"log/slog"
	"testing"

	"github.com/tez-capital/tezsign/secure"
)

type benchmarkSetup struct {
	store *FileStore
	ring  *KeyRing
	keyID string
	tz4   string
	key   *gKey
}

func newBenchmarkSetup(tb testing.TB) *benchmarkSetup {
	tb.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := NewFileStore(tb.TempDir())
	if err != nil {
		tb.Fatalf("NewFileStore: %v", err)
	}

	pass := []byte("bench-passphrase")
	tb.Cleanup(func() {
		secure.MemoryWipe(pass)
	})

	if err := store.InitMaster(); err != nil {
		tb.Fatalf("InitMaster: %v", err)
	}
	if err := store.WriteSeed(pass, false); err != nil {
		tb.Fatalf("WriteSeed: %v", err)
	}

	ring := NewKeyRing(log, store)
	keyID, _, tz4, err := ring.CreateKey("bench", pass)
	if err != nil {
		tb.Fatalf("CreateKey: %v", err)
	}
	if err := ring.Unlock(keyID, pass); err != nil {
		tb.Fatalf("Unlock: %v", err)
	}

	key := ring.get(keyID)
	if key == nil {
		tb.Fatalf("key %q not found after unlock", keyID)
	}

	tb.Cleanup(func() {
		_ = ring.Lock(keyID)
	})

	return &benchmarkSetup{
		store: store,
		ring:  ring,
		keyID: keyID,
		tz4:   tz4,
		key:   key,
	}
}

func BenchmarkStatePersist(b *testing.B) {
	b.Run("double_buffer", func(b *testing.B) {
		setup := newBenchmarkSetup(b)
		key := setup.key

		unlock := key.lock()
		state := key.keyStateSnapshot()
		file := key.hwmFile
		seq := key.hwmSeq
		dek := append([]byte(nil), key.dek...)
		tz4 := key.tz4
		unlock()
		defer secure.MemoryWipe(dek)

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			seq++
			state.ByKind[int32(BLOCK)] = &KindState{
				Level: uint64(i + 1),
				Round: uint32(i & 31),
			}
			if err := file.persist(dek, setup.keyID, tz4, state, seq); err != nil {
				b.Fatalf("persist: %v", err)
			}
		}
	})
}

func BenchmarkSignAndPersistLocal(b *testing.B) {
	for _, kind := range allSignKinds {
		kind := kind
		b.Run(kind.String()+"/double_buffer", func(b *testing.B) {
			setup := newBenchmarkSetup(b)

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				raw := buildBenchmarkPayload(kind, uint64(i+1), uint32(i&31))
				if _, err := setup.ring.SignAndUpdate(setup.tz4, raw); err != nil {
					b.Fatalf("SignAndUpdate: %v", err)
				}
			}
		})
	}
}

func buildBenchmarkPayload(kind SIGN_KIND, level uint64, round uint32) []byte {
	switch kind {
	case BLOCK:
		return buildBenchmarkBlockPayload(level, round)
	case PREATTESTATION:
		return buildBenchmarkPreattestationPayload(level, round)
	case ATTESTATION:
		return buildBenchmarkAttestationPayload(level, round)
	default:
		return nil
	}
}

func buildBenchmarkBlockPayload(level uint64, round uint32) []byte {
	const (
		chainIDLen       = 4
		levelLen         = 4
		protoLevelLen    = 1
		predLen          = 32
		tsLen            = 8
		vpLen            = 1
		ophLen           = 32
		fitnessLenField  = 4
		fitnessBlobRound = 4
	)

	total := 1 + chainIDLen + levelLen + protoLevelLen + predLen + tsLen + vpLen + ophLen + fitnessLenField + fitnessBlobRound
	buf := make([]byte, total)

	i := 0
	buf[i] = byte(BLOCK)
	i++

	copy(buf[i:i+4], []byte{0x12, 0x34, 0x56, 0x78})
	i += 4

	putBE32(buf[i:i+4], uint32(level))
	i += 4

	buf[i] = 1
	i++

	i += 32
	i += 8
	buf[i] = 4
	i++
	i += 32

	putBE32(buf[i:i+4], 4)
	i += 4

	putBE32(buf[i:i+4], round)
	return buf
}

func buildBenchmarkPreattestationPayload(level uint64, round uint32) []byte {
	const (
		chainIDLen = 4
		branchLen  = 32
		kindLen    = 1
		i32        = 4
	)

	total := 1 + chainIDLen + branchLen + kindLen + i32 + i32
	buf := make([]byte, total)

	i := 0
	buf[i] = byte(PREATTESTATION)
	i++

	copy(buf[i:i+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	i += 4

	i += 32
	buf[i] = 0x01
	i++

	putBE32(buf[i:i+4], uint32(level))
	i += 4
	putBE32(buf[i:i+4], round)

	return buf
}

func buildBenchmarkAttestationPayload(level uint64, round uint32) []byte {
	const (
		chainIDLen = 4
		branchLen  = 32
		kindLen    = 1
		i32        = 4
	)

	total := 1 + chainIDLen + branchLen + kindLen + i32 + i32
	buf := make([]byte, total)

	i := 0
	buf[i] = byte(ATTESTATION)
	i++

	copy(buf[i:i+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	i += 4

	i += 32
	buf[i] = 0x01
	i++

	putBE32(buf[i:i+4], uint32(level))
	i += 4
	putBE32(buf[i:i+4], round)

	return buf
}

func putBE32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}
