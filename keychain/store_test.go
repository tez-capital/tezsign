package keychain

import (
	"os"
	"testing"
)

func testKeyState(values map[SIGN_KIND]HighWatermark) *KeyState {
	ks := newEmptyKeyState()
	for _, kind := range allSignKinds {
		ks.ByKind[int32(kind)] = values[kind].ToKeyState()
	}
	return ks
}

func assertKeyStateEqual(t *testing.T, got *KeyState, want map[SIGN_KIND]HighWatermark) {
	t.Helper()

	for _, kind := range allSignKinds {
		state := got.GetByKind()[int32(kind)]
		if state == nil {
			t.Fatalf("missing state for kind %v", kind)
		}

		expected := want[kind]
		if state.GetLevel() != expected.level || state.GetRound() != expected.round {
			t.Fatalf("kind %v: got level=%d round=%d, want level=%d round=%d",
				kind,
				state.GetLevel(),
				state.GetRound(),
				expected.level,
				expected.round,
			)
		}
	}
}

func TestDoubleBufferWriteReadRoundTrip(t *testing.T) {
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	const (
		id  = "roundtrip"
		tz4 = "tz4-roundtrip"
	)

	if err := os.MkdirAll(fs.keyDir(id), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	dek := []byte("0123456789abcdef0123456789abcdef")

	file, ks, seq, corrupted, err := openKeyHWMFile(fs.keyStatePath(id), dek, id, tz4)
	if err != nil {
		t.Fatalf("openKeyHWMFile: %v", err)
	}
	defer file.Close()

	if corrupted {
		t.Fatalf("expected clean state")
	}
	if seq != 0 {
		t.Fatalf("expected zero seq, got %d", seq)
	}
	assertKeyStateEqual(t, ks, map[SIGN_KIND]HighWatermark{
		BLOCK:          {},
		PREATTESTATION: {},
		ATTESTATION:    {},
	})

	want := map[SIGN_KIND]HighWatermark{
		BLOCK:          {level: 42, round: 3},
		PREATTESTATION: {level: 39, round: 1},
		ATTESTATION:    {level: 40, round: 2},
	}
	if err := file.persist(dek, id, tz4, testKeyState(want), 7); err != nil {
		t.Fatalf("persist: %v", err)
	}
	file.waitIdle()

	got, gotSeq, missing, corrupted, err := file.load(dek, id, tz4)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if missing {
		t.Fatalf("expected persisted state")
	}
	if corrupted {
		t.Fatalf("expected clean double buffer")
	}
	if gotSeq != 7 {
		t.Fatalf("expected seq=7, got %d", gotSeq)
	}
	assertKeyStateEqual(t, got, want)
}

func TestDoubleBufferRecoversFromCorruptedSecondSlot(t *testing.T) {
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	const (
		id  = "corrupted-slot"
		tz4 = "tz4-corrupted-slot"
	)

	if err := os.MkdirAll(fs.keyDir(id), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	dek := []byte("abcdef0123456789abcdef0123456789")

	file, _, _, _, err := openKeyHWMFile(fs.keyStatePath(id), dek, id, tz4)
	if err != nil {
		t.Fatalf("openKeyHWMFile: %v", err)
	}
	defer file.Close()

	want := map[SIGN_KIND]HighWatermark{
		BLOCK:          {level: 11, round: 1},
		PREATTESTATION: {level: 12, round: 2},
		ATTESTATION:    {level: 13, round: 3},
	}
	if err := file.persist(dek, id, tz4, testKeyState(want), 3); err != nil {
		t.Fatalf("persist: %v", err)
	}
	file.waitIdle()

	var corruptByte [1]byte
	if _, err := file.file.ReadAt(corruptByte[:], keyStateSlotSize+8); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	corruptByte[0] ^= 0xff
	if _, err := file.file.WriteAt(corruptByte[:], keyStateSlotSize+8); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}

	got, gotSeq, missing, corrupted, err := file.load(dek, id, tz4)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if missing {
		t.Fatalf("expected persisted state")
	}
	if !corrupted {
		t.Fatalf("expected corruption flag")
	}
	if gotSeq != 3 {
		t.Fatalf("expected seq=3, got %d", gotSeq)
	}
	assertKeyStateEqual(t, got, want)
}

func TestDoubleBufferPrefersNewerSequence(t *testing.T) {
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	const (
		id  = "newer-seq"
		tz4 = "tz4-newer-seq"
	)

	if err := os.MkdirAll(fs.keyDir(id), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	dek := []byte("fedcba9876543210fedcba9876543210")

	file, _, _, _, err := openKeyHWMFile(fs.keyStatePath(id), dek, id, tz4)
	if err != nil {
		t.Fatalf("openKeyHWMFile: %v", err)
	}
	defer file.Close()

	older := map[SIGN_KIND]HighWatermark{
		BLOCK:          {level: 20, round: 1},
		PREATTESTATION: {level: 20, round: 1},
		ATTESTATION:    {level: 20, round: 1},
	}
	newer := map[SIGN_KIND]HighWatermark{
		BLOCK:          {level: 21, round: 0},
		PREATTESTATION: {level: 22, round: 0},
		ATTESTATION:    {level: 23, round: 0},
	}

	var slotA [keyStateSlotSize]byte
	if err := encodeKeyStateSlot(slotA[:], dek, id, tz4, testKeyState(newer), 2); err != nil {
		t.Fatalf("encodeKeyStateSlot A: %v", err)
	}
	if _, err := file.file.WriteAt(slotA[:], 0); err != nil {
		t.Fatalf("WriteAt slot A: %v", err)
	}

	var slotB [keyStateSlotSize]byte
	if err := encodeKeyStateSlot(slotB[:], dek, id, tz4, testKeyState(older), 1); err != nil {
		t.Fatalf("encodeKeyStateSlot B: %v", err)
	}
	if _, err := file.file.WriteAt(slotB[:], keyStateSlotSize); err != nil {
		t.Fatalf("WriteAt slot B: %v", err)
	}

	got, gotSeq, missing, corrupted, err := file.load(dek, id, tz4)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if missing {
		t.Fatalf("expected persisted state")
	}
	if corrupted {
		t.Fatalf("expected clean double buffer")
	}
	if gotSeq != 2 {
		t.Fatalf("expected seq=2, got %d", gotSeq)
	}
	assertKeyStateEqual(t, got, newer)
}
