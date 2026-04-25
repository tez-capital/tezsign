package keychain

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tez-capital/tezsign/secure"
	"github.com/tez-capital/tezsign/signer"
	"github.com/tez-capital/tezsign/signerpb"
)

type Keys struct {
	mu   sync.RWMutex
	byID map[string]*gKey
}

type keyEntry struct {
	id  string
	key *gKey
}

func (keys *Keys) Load(id string) (*gKey, bool) {
	keys.mu.RLock()
	key, ok := keys.byID[id]
	keys.mu.RUnlock()
	return key, ok
}

func (keys *Keys) Insert(id string, key *gKey) bool {
	keys.mu.Lock()
	defer keys.mu.Unlock()

	if _, ok := keys.byID[id]; ok {
		return false
	}
	if keys.byID == nil {
		keys.byID = make(map[string]*gKey)
	}

	keys.byID[id] = key
	return true
}

func (keys *Keys) LoadAndDelete(id string) (*gKey, bool) {
	keys.mu.Lock()
	defer keys.mu.Unlock()

	key, ok := keys.byID[id]
	if !ok {
		return nil, false
	}
	delete(keys.byID, id)
	return key, true
}

func (keys *Keys) Range(fn func(id string, key *gKey) bool) {
	keys.mu.RLock()
	entries := make([]keyEntry, 0, len(keys.byID))
	for id, key := range keys.byID {
		entries = append(entries, keyEntry{id: id, key: key})
	}
	keys.mu.RUnlock()

	for _, entry := range entries {
		if !fn(entry.id, entry.key) {
			return
		}
	}
}

// gKey: device-held key state (per key, per proto kind).
type gKey struct {
	mu sync.Mutex // per-key lock

	// in-memory working material (present only while "unlocked")
	dek       []byte // 32B per-key data encryption key (wrapped by master on disk)
	encSecret []byte // ciphertext of 32B LE scalar (AES-GCM with DEK)
	dataNonce []byte // 12B AES-GCM nonce for encSecret

	// AAD binding (needed at decrypt time to authenticate metadata)
	blPubkey string
	tz4      string

	watermark map[SIGN_KIND]HighWatermark
	hwmFile   *keyHWMFile
	hwmSeq    uint64

	hwmCorrupted bool
}

func newWatermarks() map[SIGN_KIND]HighWatermark {
	watermarks := make(map[SIGN_KIND]HighWatermark, len(allSignKinds))
	for _, kind := range allSignKinds {
		watermarks[kind] = HighWatermark{}
	}
	return watermarks
}

func newGKey(blPubkey, tz4 string) *gKey {
	return &gKey{
		blPubkey:  blPubkey,
		tz4:       tz4,
		watermark: newWatermarks(),
	}
}

func (k *gKey) lock() func() {
	k.mu.Lock()
	return func() {
		k.mu.Unlock()
	}
}

func (k *gKey) matchesTz4(tz4 string) bool {
	unlock := k.lock()
	defer unlock()
	return k.tz4 == tz4
}

func (k *gKey) markHWMCorrupted(corrupted bool) {
	unlock := k.lock()
	defer unlock()
	k.hwmCorrupted = corrupted
}

func (k *gKey) unlock(log *slog.Logger, store *FileStore, id string, masterPassword []byte) error {
	dek, encSecret, dataNonce, blPubkey, tz4, err := store.unlock(id, masterPassword)
	if err != nil {
		return err
	}
	if len(dek) != 32 {
		secure.MemoryWipe(dek)
		return fmt.Errorf("load state: bad DEK length %d", len(dek))
	}

	hwmFile, keyState, hwmSeq, corrupted, err := openKeyHWMFile(store.keyStatePath(id), dek, id, tz4)
	if err != nil {
		if errors.Is(err, ErrKeyStateCorrupted) {
			k.markHWMCorrupted(true)
		}
		secure.MemoryWipe(dek)
		return fmt.Errorf("load state: %w", err)
	}

	unlock := k.lock()
	defer unlock()

	if err := k.closeHWMFile(); err != nil {
		log.Warn("close hwm file", "key", id, "err", err)
	}
	k.clearSensitiveMaterial()

	k.dek = dek
	k.encSecret = encSecret
	k.dataNonce = dataNonce
	k.blPubkey = blPubkey
	k.tz4 = tz4
	k.hwmFile = hwmFile
	k.hwmSeq = hwmSeq
	k.hwmCorrupted = corrupted
	k.applyKeyState(keyState)

	return nil
}

func (k *gKey) lockKey() error {
	unlock := k.lock()
	defer unlock()

	k.clearSensitiveMaterial()
	return k.closeHWMFile()
}

func (k *gKey) dispose(log *slog.Logger, id string) {
	unlock := k.lock()
	defer unlock()

	k.clearSensitiveMaterial()
	if err := k.closeHWMFile(); err != nil {
		log.Warn("close hwm file", "key", id, "err", err)
	}
}

func (k *gKey) populateStatus(id string, status *signerpb.KeyStatus, log *slog.Logger) {
	unlock := k.lock()
	defer unlock()

	isUnlocked := k.isUnlocked()
	if isUnlocked {
		if ksDisk, seqDisk, missingState, corrupted, err := k.hwmFile.load(k.dek, id, k.tz4); err != nil {
			if errors.Is(err, ErrKeyStateCorrupted) {
				k.hwmCorrupted = true
			} else {
				log.Error("status: check state", "key", id, "err", err)
			}
		} else {
			k.hwmSeq = seqDisk
			switch {
			case corrupted:
				k.hwmCorrupted = true
				k.resetWatermarks()
			case missingState:
				k.hwmCorrupted = false
				k.resetWatermarks()
				k.hwmSeq = 0
			default:
				k.hwmCorrupted = false
				k.applyKeyState(ksDisk)
			}
		}
	}

	if k.hwmCorrupted && isUnlocked {
		status.StateCorrupted = true
		return
	}
	if !isUnlocked {
		return
	}

	status.LockState = signerpb.LockState_UNLOCKED
	block := k.watermark[BLOCK]
	preattestation := k.watermark[PREATTESTATION]
	attestation := k.watermark[ATTESTATION]

	status.LastBlockLevel = block.level
	status.LastPreattestationLevel = preattestation.level
	status.LastAttestationLevel = attestation.level

	status.LastBlockRound = block.round
	status.LastPreattestationRound = preattestation.round
	status.LastAttestationRound = attestation.round
}

func (k *gKey) signAndUpdate(keyID string, raw []byte) ([]byte, error) {
	knd, level, round, signBytes, err := DecodeAndValidateSignPayload(raw)
	if err != nil {
		return nil, ErrBadPayload
	}

	unlock := k.lock()
	defer unlock()

	if k.dek == nil || k.encSecret == nil || k.dataNonce == nil {
		return nil, ErrKeyLocked
	}
	if k.hwmFile == nil {
		return nil, fmt.Errorf("high-watermark file is not open")
	}
	if k.hwmCorrupted {
		return nil, ErrKeyStateCorrupted
	}

	prev := k.watermark[knd]
	if !(level > prev.level || (level == prev.level && round > prev.round)) {
		return nil, ErrStaleWatermark
	}

	nextState := k.keyStateSnapshot()
	nextState.ByKind[int32(knd)] = &KindState{
		Level: level,
		Round: round,
	}
	nextSeq := k.hwmSeq + 1

	k.hwmFile.persistAsync(k.dek, keyID, k.tz4, nextState, nextSeq)

	gcmDEK, err := newAESGCM(k.dek)
	if err != nil {
		return nil, err
	}
	aad := []byte("bl=" + k.blPubkey + "|tz4=" + k.tz4)

	le, err := gcmDEK.Open(nil, k.dataNonce, k.encSecret, aad)
	if err != nil {
		return nil, fmt.Errorf("corrupted key (secret)")
	}
	if len(le) != 32 {
		secure.MemoryWipe(le)
		return nil, fmt.Errorf("secret length invalid")
	}

	var sk signer.SecretKey
	if sk.FromLEndian(le) == nil {
		secure.MemoryWipe(le)
		return nil, fmt.Errorf("invalid scalar")
	}

	sig, _ := signer.SignCompressed(&sk, signBytes)
	secure.MemoryWipe(le)
	sk.Zeroize()

	if err := k.hwmFile.waitPersist(); err != nil {
		return nil, fmt.Errorf("persist state: %w", err)
	}

	k.watermark[knd] = HighWatermark{level: level, round: round}
	k.hwmSeq = nextSeq
	k.hwmCorrupted = false

	return sig, nil
}

func (k *gKey) setLevel(id string, level uint64) error {
	unlock := k.lock()
	defer unlock()

	if k.dek == nil {
		return fmt.Errorf("key locked")
	}
	if k.hwmFile == nil {
		return fmt.Errorf("high-watermark file is not open")
	}

	for _, kind := range allSignKinds {
		current := k.watermark[kind].level
		if level <= current {
			return fmt.Errorf("level must be greater than current %s level (current=%d)", kind, current)
		}
	}

	nextState := k.keyStateSnapshot()
	for _, kind := range allSignKinds {
		nextState.ByKind[int32(kind)] = &KindState{
			Level: level,
			Round: 0,
		}
	}
	nextSeq := k.hwmSeq + 1

	if err := k.hwmFile.persist(k.dek, id, k.tz4, nextState, nextSeq); err != nil {
		return err
	}

	for _, kind := range allSignKinds {
		k.watermark[kind] = HighWatermark{level: level, round: 0}
	}
	k.hwmSeq = nextSeq
	k.hwmCorrupted = false
	return nil
}

// These helpers operate on the current key state; callers establish locking.
func (k *gKey) resetWatermarks() {
	for _, kind := range allSignKinds {
		k.watermark[kind] = HighWatermark{}
	}
}

func (k *gKey) applyKeyState(ks *KeyState) {
	k.resetWatermarks()
	if ks == nil || ks.ByKind == nil {
		return
	}
	for _, kind := range allSignKinds {
		if st, ok := ks.ByKind[int32(kind)]; ok && st != nil {
			k.watermark[kind] = HighWatermark{level: st.Level, round: st.Round}
		}
	}
}

func (k *gKey) GetKeyState() *KeyState {
	unlock := k.lock()
	defer unlock()
	return k.keyStateSnapshot()
}

func (k *gKey) keyStateSnapshot() *KeyState {
	ks := &KeyState{ByKind: map[int32]*KindState{}}
	for _, sk := range allSignKinds {
		ks.ByKind[int32(sk)] = k.watermark[sk].ToKeyState()
	}
	return ks
}

func (k *gKey) isUnlocked() bool {
	return k.dek != nil && k.encSecret != nil && k.dataNonce != nil && k.hwmFile != nil
}

func (k *gKey) clearSensitiveMaterial() {
	if k.dek != nil {
		secure.MemoryWipe(k.dek)
		k.dek = nil
	}
	k.encSecret = nil
	k.dataNonce = nil
	k.hwmSeq = 0
}

func (k *gKey) closeHWMFile() error {
	if k.hwmFile == nil {
		return nil
	}
	err := k.hwmFile.Close()
	k.hwmFile = nil
	return err
}
