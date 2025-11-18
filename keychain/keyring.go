package keychain

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tez-capital/tezsign/logging"
	"github.com/tez-capital/tezsign/signer"
)

type SIGN_KIND byte

const (
	UNSPECIFIED    SIGN_KIND = 0x00
	BLOCK          SIGN_KIND = 0x11
	PREATTESTATION SIGN_KIND = 0x12
	ATTESTATION    SIGN_KIND = 0x13
)

type HighWatermark struct {
	level uint64
	round uint32
}

func (hw HighWatermark) ToKeyState() *KindState {
	return &KindState{
		Level: hw.level,
		Round: hw.round,
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

	stateCorrupted bool
}

func (k *gKey) ensureWatermarksLocked() {
	if k.watermark == nil {
		k.watermark = make(map[SIGN_KIND]HighWatermark, len(signKinds()))
	}
}

func (k *gKey) resetWatermarksLocked() {
	k.ensureWatermarksLocked()
	for _, kind := range signKinds() {
		k.watermark[kind] = HighWatermark{}
	}
}

func (k *gKey) applyKeyStateLocked(ks *KeyState) {
	k.ensureWatermarksLocked()
	if ks == nil || ks.ByKind == nil {
		k.resetWatermarksLocked()
		return
	}
	for _, kind := range signKinds() {
		if st, ok := ks.ByKind[int32(kind)]; ok && st != nil {
			k.watermark[kind] = HighWatermark{level: st.Level, round: st.Round}
		} else {
			k.watermark[kind] = HighWatermark{}
		}
	}
}

func (k *gKey) GetKeyState() *KeyState {
	ks := &KeyState{ByKind: map[int32]*KindState{}}
	for _, sk := range signKinds() {
		ks.ByKind[int32(sk)] = k.watermark[sk].ToKeyState()
	}
	return ks
}

type KeyRing struct {
	keys   sync.Map      // map[string]*gKey
	nextID atomic.Uint64 // atomic counter for auto key ids (key1, key2, ...)
	log    *slog.Logger
	store  *FileStore
}

func NewKeyRing(log *slog.Logger, store *FileStore) *KeyRing {
	if log == nil {
		log, _ = logging.NewFromEnv()
	}

	return &KeyRing{log: log, store: store}
}

func (kr *KeyRing) CreateKey(wanted string, masterPassword []byte) (id, blPubkey, tz4 string, err error) {
	id = normalizeID(wanted)

	if id != "" && !isValidID(id) {
		return "", "", "", fmt.Errorf("invalid key_id")
	}

	// --- Decide deterministic vs random ---
	enabled, seed, seedErr := kr.store.readSeed(masterPassword)
	defer MemoryWipe(seed)
	if seedErr != nil {
		return "", "", "", seedErr
	}

	useDeterministic := enabled

	var mf *masterFile
	if useDeterministic {
		mf, err = kr.store.readMaster()
		if err != nil {
			return "", "", "", err
		}
	}

	var deterministicIndex uint32
	var detIndexReserved bool

	for {
		candidate := id
		if candidate == "" {
			n := kr.nextID.Add(1)
			candidate = fmt.Sprintf("key%d", n)
		}

		if kr.store.hasKey(candidate) {
			if id != "" {
				return "", "", "", ErrKeyExists
			}
			continue
		}

		var (
			secretKey   *signer.SecretKey
			pubkeyBytes []byte
			popBLsig    string
			index       uint32
		)
		if useDeterministic {
			if !detIndexReserved {
				deterministicIndex, err = kr.store.nextDeterministicIndex()
				if err != nil {
					return "", "", "", err
				}
				detIndexReserved = true
			}
			index = deterministicIndex
			// Build HD params (domain-separated with store salt)
			secretKey, pubkeyBytes, blPubkey, err = signer.GenerateHDKey(mf.Salt, seed, index)
			if err != nil {
				return "", "", "", err
			}
			tz4, _ = signer.Tz4FromBLPubkeyBytes(pubkeyBytes)
		} else {
			// legacy random
			secretKey, pubkeyBytes, blPubkey = signer.GenerateRandomKey()
			tz4, _ = signer.Tz4FromBLPubkeyBytes(pubkeyBytes)
		}

		_, popBLsig, err = signer.SignPoPCompressed(secretKey, pubkeyBytes)
		if err != nil {
			return "", "", "", err
		}

		// persist (runs Argon2id exactly once)
		func() {
			skLE := secretKey.ToLEndian()
			defer MemoryWipe(skLE)

			pErr := kr.store.createKey(candidate, masterPassword, skLE, blPubkey, tz4, popBLsig)
			if pErr == nil {
				id = candidate
				err = nil
				return
			}

			if errors.Is(pErr, ErrKeyExists) {
				err = ErrKeyExists
				return
			}

			err = pErr
		}()

		if err != nil {
			if id != "" {
				return "", "", "", err
			} // explicit id error

			if errors.Is(err, ErrKeyExists) {
				err = nil
				continue // try next auto id (nextID.Add(1) will advance)
			}

			return "", "", "", err
		}

		// success: populate in-memory state
		wm := make(map[SIGN_KIND]HighWatermark, 3)
		for _, k := range signKinds() {
			wm[k] = HighWatermark{level: 0, round: 0}
		}

		newKey := &gKey{blPubkey: blPubkey, tz4: tz4, watermark: wm}
		if _, loaded := kr.keys.LoadOrStore(id, newKey); loaded {
			return "", "", "", ErrKeyExists
		}
		kr.log.Info(fmt.Sprintf("NEWKEY id=%s tz4=%s deterministic=%v index=%d", id, tz4, useDeterministic, index))
		return id, blPubkey, tz4, nil
	}
}

func (kr *KeyRing) Unlock(id string, masterPassword []byte) error {
	// 1) load materials from disk
	dek, enc, nonce, blPubkey, tz4, err := kr.store.unlock(id, masterPassword)
	if err != nil {
		return err
	}

	// 2) ensure we have a gKey entry (create empty if missing)
	v, _ := kr.keys.LoadOrStore(id, &gKey{})
	key := v.(*gKey)

	key.mu.Lock()
	defer key.mu.Unlock()

	// (optional) sanity
	if len(dek) != 32 {
		MemoryWipe(dek)
		return fmt.Errorf("load state: bad DEK length %d", len(dek))
	}

	// 3) load level.bin (protobuf with map<int32, KindState>)
	ks, missing, corrupted, err := kr.store.readKeyState(id, dek, tz4)
	if err != nil {
		if errors.Is(err, ErrKeyStateCorrupted) {
			key.stateCorrupted = true
		}
		MemoryWipe(dek)
		return fmt.Errorf("load state: %w", err)
	}
	key.stateCorrupted = corrupted

	if ks.ByKind == nil {
		ks.ByKind = map[int32]*KindState{}
	}

	// 4) attach sensitive material only after successful state read
	if key.dek != nil {
		MemoryWipe(key.dek)
	}
	key.dek, key.encSecret, key.dataNonce = dek, enc, nonce
	key.blPubkey, key.tz4 = blPubkey, tz4

	// 5) ensure watermark map exists and populate from disk (default zeros)
	key.applyKeyStateLocked(ks)
	if missing {
		key.resetWatermarksLocked()
	}

	kr.log.Info("key unlocked", "key", id)

	return nil
}

func (kr *KeyRing) Lock(id string) error {
	key := kr.get(id)
	if key == nil {
		return fmt.Errorf("unknown key")
	}

	key.mu.Lock()
	if key.dek != nil {
		MemoryWipe(key.dek)
		key.dek = nil
	}
	key.encSecret = nil
	key.dataNonce = nil
	key.mu.Unlock()

	kr.log.Info("key locked", "key", id)

	return nil
}

func (kr *KeyRing) DeleteKey(wanted string) error {
	id := normalizeID(wanted)
	if id == "" {
		return fmt.Errorf("invalid key_id")
	}
	if !kr.store.hasKey(id) {
		return ErrKeyNotFound
	}

	if v, ok := kr.keys.LoadAndDelete(id); ok {
		if key, _ := v.(*gKey); key != nil {
			key.mu.Lock()
			if key.dek != nil {
				MemoryWipe(key.dek)
				key.dek = nil
			}
			key.encSecret = nil
			key.dataNonce = nil
			key.mu.Unlock()
		}
	}

	return kr.store.removeKey(id)
}

func (kr *KeyRing) VerifyMasterPassword(masterPassword []byte) error {
	_, seed, err := kr.store.readSeed(masterPassword)
	if err != nil {
		return err
	}
	MemoryWipe(seed)
	return nil
}

func (kr *KeyRing) Status() []*signer.KeyStatus {
	ids, err := kr.store.list()
	if err != nil {
		kr.log.Error("status list", "err", err)
		return nil
	}

	out := make([]*signer.KeyStatus, 0, len(ids))
	for _, id := range ids {
		ks := &signer.KeyStatus{KeyId: id}

		// Always read identity + PoP from disk
		meta, mErr := kr.store.readKeyMeta(id)
		if mErr != nil {
			kr.log.Error("status: read meta", "key", id, "err", mErr)
		}

		ks.LockState = signer.LockState_LOCKED
		ks.Tz4 = meta.TZ4
		ks.BlPubkey = meta.BLPubkey
		ks.Pop = meta.Pop

		// If key is present + unlocked, include watermarks
		if key := kr.get(id); key != nil {
			key.mu.Lock()
			isUnlocked := (key.dek != nil && key.encSecret != nil && key.dataNonce != nil)

			if isUnlocked {
				if ksDisk, missingState, corrupted, err := kr.store.readKeyState(id, key.dek, key.tz4); err != nil {
					if errors.Is(err, ErrKeyStateCorrupted) {
						key.stateCorrupted = true
					} else {
						kr.log.Error("status: check state", "key", id, "err", err)
					}
				} else {
					switch {
					case corrupted:
						key.stateCorrupted = true
						key.resetWatermarksLocked()
					case missingState:
						key.stateCorrupted = false
						key.resetWatermarksLocked()
					default:
						key.stateCorrupted = false
						key.applyKeyStateLocked(ksDisk)
					}
				}
			}

			showCorrupted := key.stateCorrupted && isUnlocked

			if showCorrupted {
				ks.StateCorrupted = true
			} else if isUnlocked {
				ks.LockState = signer.LockState_UNLOCKED
				block := key.watermark[BLOCK]
				preattestation := key.watermark[PREATTESTATION]
				attestation := key.watermark[ATTESTATION]

				// ----
				ks.LastBlockLevel = block.level
				ks.LastPreattestationLevel = preattestation.level
				ks.LastAttestationLevel = attestation.level

				ks.LastBlockRound = block.round
				ks.LastPreattestationRound = preattestation.round
				ks.LastAttestationRound = attestation.round
			}
			key.mu.Unlock()
		}
		out = append(out, ks)
	}

	return out
}

// resolveKeyIDByTZ4 scans the store to find the key id for a given tz4.
// Works whether the key is locked or unlocked.
func (kr *KeyRing) resolveKeyIDByTZ4(tz4 string) (string, error) {
	if strings.TrimSpace(tz4) == "" {
		return "", fmt.Errorf("empty tz4")
	}
	ids, err := kr.store.list()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		meta, mErr := kr.store.readKeyMeta(id)
		if mErr != nil {
			kr.log.Error("resolve tz4: read meta", "key", id, "err", mErr)
			continue
		}
		if meta.TZ4 == tz4 {
			return id, nil
		}
	}
	return "", fmt.Errorf("unknown tz4")
}

// SignAndUpdate validates key state + monotonic (level, round) and signs.
// Monotonic rule: (level > lastLevel) OR (level == lastLevel && round > lastRound)
func (kr *KeyRing) SignAndUpdate(tz4 string, raw []byte) (sig []byte, err error) {
	knd, level, round, signBytes, err := DecodeAndValidateSignPayload(raw)
	if err != nil {
		return nil, ErrBadPayload
	}

	keyID, key := kr.getByTz4(tz4)
	if key == nil {
		return nil, ErrKeyNotFound
	}

	key.mu.Lock()
	defer key.mu.Unlock()

	if key.dek == nil || key.encSecret == nil || key.dataNonce == nil {
		return nil, ErrKeyLocked
	}

	// Monotonicity
	prev := key.watermark[knd]
	if !(level > prev.level || (level == prev.level && round > prev.round)) {
		return nil, ErrStaleWatermark
	}

	// decrypt secret (32B LE) using in-memory DEK; authenticate with AAD
	gcmDEK, err := newAESGCM(key.dek)
	if err != nil {
		return nil, err
	}
	aad := []byte("bl=" + key.blPubkey + "|tz4=" + key.tz4)

	le, err := gcmDEK.Open(nil, key.dataNonce, key.encSecret, aad)
	if err != nil {
		key.mu.Unlock()
		return nil, fmt.Errorf("corrupted key (secret)")
	}
	if len(le) != 32 {
		MemoryWipe(le)
		return nil, fmt.Errorf("secret length invalid")
	}

	// build blst.SecretKey from LE just for this sign
	var sk signer.SecretKey
	if sk.FromLEndian(le) == nil {
		MemoryWipe(le)
		return nil, fmt.Errorf("invalid scalar")
	}

	writeChan := make(chan error, 1)
	go func() {
		// Update in-memory

		key.watermark[knd] = HighWatermark{level: level, round: round}
		// Persist level.bin using DEK
		if err := kr.store.writeKeyState(keyID, key.dek, key.tz4, key.GetKeyState()); err != nil {
			writeChan <- fmt.Errorf("persist state: %w", err)
			return
		}

		key.stateCorrupted = false
		writeChan <- nil
	}()

	sig, _ = signer.SignCompressed(&sk, signBytes)
	MemoryWipe(le)
	sk.Zeroize()
	err = <-writeChan
	if err != nil {
		return nil, err
	}

	return sig, nil
}

func (kr *KeyRing) SetLevel(id string, level uint64) error {
	key := kr.get(id)
	if key == nil {
		if kr.store.hasKey(id) {
			return fmt.Errorf("key locked")
		}
		return fmt.Errorf("unknown key")
	}

	key.mu.Lock()
	defer key.mu.Unlock()

	if key.dek == nil {
		return fmt.Errorf("key locked")
	}

	key.ensureWatermarksLocked()

	for _, kind := range signKinds() {
		current := key.watermark[kind].level
		if level <= current {
			return fmt.Errorf("level must be greater than current %s level (current=%d)", signKindName(kind), current)
		}
	}

	// Set level, reset round = 0
	for _, k := range signKinds() {
		key.watermark[k] = HighWatermark{level: level, round: 0}
	}

	if err := kr.store.writeKeyState(id, key.dek, key.tz4, key.GetKeyState()); err != nil {
		return err
	}
	key.stateCorrupted = false
	return nil
}

func (kr *KeyRing) get(id string) *gKey {
	v, ok := kr.keys.Load(id)
	if !ok {
		return nil
	}
	key, _ := v.(*gKey)

	return key
}

func (kr *KeyRing) getByTz4(tz4 string) (string, *gKey) {
	// TODO: optimize with a secondary map if needed
	var foundKey *gKey
	var foundID string
	kr.keys.Range(func(key, value any) bool {
		k, _ := value.(*gKey)
		if k.tz4 == tz4 {
			foundKey = k
			foundID = key.(string)
			return false
		}
		return true
	})
	return foundID, foundKey
}

func signKinds() []SIGN_KIND {
	return []SIGN_KIND{BLOCK, PREATTESTATION, ATTESTATION}
}

func signKindName(sk SIGN_KIND) string {
	switch sk {
	case BLOCK:
		return "block"
	case PREATTESTATION:
		return "preattestation"
	case ATTESTATION:
		return "attestation"
	default:
		return "unknown"
	}
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

var idRE = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func isValidID(id string) bool {
	return idRE.MatchString(id)
}
