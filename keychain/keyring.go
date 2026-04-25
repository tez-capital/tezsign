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
	"github.com/tez-capital/tezsign/secure"
	"github.com/tez-capital/tezsign/signer"
	"github.com/tez-capital/tezsign/signerpb"
)

type SIGN_KIND byte

const (
	UNSPECIFIED    SIGN_KIND = 0x00
	BLOCK          SIGN_KIND = 0x11
	PREATTESTATION SIGN_KIND = 0x12
	ATTESTATION    SIGN_KIND = 0x13
)

var allSignKinds = [...]SIGN_KIND{BLOCK, PREATTESTATION, ATTESTATION}

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

func (sk SIGN_KIND) String() string {
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

type KeyRing struct {
	lifecycleMu sync.Mutex
	keys        Keys
	nextID      atomic.Uint64 // atomic counter for auto key ids (key1, key2, ...)
	log         *slog.Logger
	store       *FileStore
}

func NewKeyRing(log *slog.Logger, store *FileStore) *KeyRing {
	if log == nil {
		log, _ = logging.NewFromEnv()
	}

	return &KeyRing{log: log, store: store}
}

func (kr *KeyRing) CreateKey(wanted string, masterPassword []byte) (id, blPubkey, tz4 string, err error) {
	kr.lifecycleMu.Lock()
	defer kr.lifecycleMu.Unlock()

	id = normalizeID(wanted)

	if id != "" && !isValidID(id) {
		return "", "", "", fmt.Errorf("invalid key_id")
	}

	// --- Decide deterministic vs random ---
	enabled, seed, seedErr := kr.store.readSeed(masterPassword)
	defer secure.MemoryWipe(seed)
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
			defer secure.MemoryWipe(skLE)

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
		if !kr.keys.Insert(id, newGKey(blPubkey, tz4)) {
			return "", "", "", ErrKeyExists
		}
		kr.log.Info(fmt.Sprintf("NEWKEY id=%s tz4=%s deterministic=%v index=%d", id, tz4, useDeterministic, index))
		return id, blPubkey, tz4, nil
	}
}

func (kr *KeyRing) Unlock(id string, masterPassword []byte) error {
	kr.lifecycleMu.Lock()
	defer kr.lifecycleMu.Unlock()

	key := kr.get(id)
	if key == nil {
		loadedKey := newGKey("", "")
		if err := loadedKey.unlock(kr.log, kr.store, id, masterPassword); err != nil {
			return err
		}

		if !kr.keys.Insert(id, loadedKey) {
			loadedKey.dispose(kr.log, id)
			return fmt.Errorf("key already present in registry")
		}
	} else {
		if err := key.unlock(kr.log, kr.store, id, masterPassword); err != nil {
			return err
		}
	}

	kr.log.Info("key unlocked", "key", id)

	return nil
}

func (kr *KeyRing) Lock(id string) error {
	key := kr.get(id)
	if key == nil {
		return fmt.Errorf("unknown key")
	}

	if err := key.lockKey(); err != nil {
		return err
	}

	kr.log.Info("key locked", "key", id)

	return nil
}

func (kr *KeyRing) DeleteKey(wanted string) error {
	kr.lifecycleMu.Lock()
	defer kr.lifecycleMu.Unlock()

	id := normalizeID(wanted)
	if id == "" {
		return fmt.Errorf("invalid key_id")
	}
	if !kr.store.hasKey(id) {
		return ErrKeyNotFound
	}

	if key, ok := kr.keys.LoadAndDelete(id); ok {
		key.dispose(kr.log, id)
	}

	return kr.store.removeKey(id)
}

func (kr *KeyRing) VerifyMasterPassword(masterPassword []byte) error {
	_, seed, err := kr.store.readSeed(masterPassword)
	if err != nil {
		return err
	}
	secure.MemoryWipe(seed)
	return nil
}

func (kr *KeyRing) Status() []*signerpb.KeyStatus {
	ids, err := kr.store.list()
	if err != nil {
		kr.log.Error("status list", "err", err)
		return nil
	}

	out := make([]*signerpb.KeyStatus, 0, len(ids))
	for _, id := range ids {
		ks := &signerpb.KeyStatus{KeyId: id}

		// Always read identity + PoP from disk
		meta, mErr := kr.store.readKeyMeta(id)
		if mErr != nil {
			kr.log.Error("status: read meta", "key", id, "err", mErr)
		}

		ks.LockState = signerpb.LockState_LOCKED
		ks.Tz4 = meta.TZ4
		ks.BlPubkey = meta.BLPubkey
		ks.Pop = meta.Pop

		// If key is present + unlocked, include watermarks
		if key := kr.get(id); key != nil {
			key.populateStatus(id, ks, kr.log)
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
	keyID, key := kr.getByTz4(tz4)
	if key == nil {
		return nil, ErrKeyNotFound
	}

	return key.signAndUpdate(keyID, raw)
}

func (kr *KeyRing) SetLevel(id string, level uint64) error {
	key := kr.get(id)
	if key == nil {
		if kr.store.hasKey(id) {
			return fmt.Errorf("key locked")
		}
		return fmt.Errorf("unknown key")
	}

	return key.setLevel(id, level)
}

func (kr *KeyRing) get(id string) *gKey {
	key, ok := kr.keys.Load(id)
	if !ok {
		return nil
	}

	return key
}

func (kr *KeyRing) getByTz4(tz4 string) (string, *gKey) {
	// TODO: optimize with a secondary map if needed
	var foundKey *gKey
	var foundID string
	kr.keys.Range(func(id string, key *gKey) bool {
		if key.matchesTz4(tz4) {
			foundKey = key
			foundID = id
			return false
		}
		return true
	})
	return foundID, foundKey
}

func normalizeID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

var idRE = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func isValidID(id string) bool {
	return idRE.MatchString(id)
}
