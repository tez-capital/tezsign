package keychain

import (
	"crypto/aes"
	"crypto/cipher"
	crypto_rand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/tez-capital/tezsign/secure"
	"golang.org/x/crypto/argon2"
	"google.golang.org/protobuf/proto"
)

const (
	storeFormatVersion = 1
	masterFileName     = "master.json"
	seedFileName       = "seed.bin" // [1 flag byte][12 nonce][GCM(seed32)]
	keysDirName        = "keys"
	keyMetaFileName    = "meta.json"
	keyBinFileName     = "encrypted.bin"
	keyStateFileName   = "level.bin"

	tmpSuffix = ".tmp"

	keyStateSlotSize     = 100
	keyStateSlotDataSize = keyStateSlotSize - 4
	keyStateFileSize     = 2 * keyStateSlotSize
	keyStateNonceSize    = 12
	keyStatePlainSize    = 68
)

var (
	ErrKeyExists                    = errors.New("key_id already exists")
	ErrMasterJSONAlreadyInitialized = errors.New("master json already initialized")
	ErrKeyStateCorrupted            = errors.New("state corrupted")

	keyStateMagic = [4]byte{'H', 'W', 'M', '1'}
)

type keyStateFormat uint8

const (
	keyStateFormatMissing keyStateFormat = iota
	keyStateFormatDoubleBuffer
	keyStateFormatLegacy
)

type FileStore struct {
	base     string
	masterMu sync.Mutex
}

// ----- on-disk formats -----

type masterFile struct {
	Version                int          `json:"version"`
	Salt                   []byte       `json:"salt"` // Argon2id salt
	Params                 argon2Params `json:"params"`
	Created                time.Time    `json:"created"`
	NextDeterministicIndex uint64       `json:"next_det_index,omitempty"`
}

type argon2Params struct {
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"` // KiB
	Threads uint8  `json:"threads"`
	KeyLen  uint32 `json:"key_len"`
}

type keyMeta struct {
	Version  int       `json:"version"`
	KeyID    string    `json:"key_id"`
	TZ4      string    `json:"tz4"`
	BLPubkey string    `json:"bl_pubkey"`
	Pop      string    `json:"pop"` // BLsig…
	Created  time.Time `json:"created"`
	// nonces are per-ciphertext
	WrapNonce []byte `json:"wrap_nonce"` // for wrapped DEK (with KEK)
	DataNonce []byte `json:"data_nonce"` // for encrypted secret (with DEK)

}

type keyBundle struct {
	// binary blobs; you can also inline base64 into keyMeta if you prefer single JSON file
	WrappedDEK []byte // AES-GCM(KEK, DEK, WrapNonce, AAD=id|tz4)
	EncSecret  []byte // AES-GCM(DEK, skLE32, DataNonce, AAD=blpubkey|tz4)
}

// ----- helpers -----

func mkDirs(base string) error {
	return os.MkdirAll(filepath.Join(base, keysDirName), 0o700)
}

func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

func writeJSONAtomic(path string, v any, perm os.FileMode) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, b, perm)
}

func writeBytesAtomic(path string, b []byte, perm os.FileMode) error {
	tmp := path + tmpSuffix
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_SYNC, perm)
	if err != nil {
		return err
	}
	if _, err := file.Write(b); err != nil {
		file.Close()
		return err
	}
	file.Close()
	return os.Rename(tmp, path)
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = io.ReadFull(crypto_rand.Reader, b)
	return b
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ----- FileStore API -----

func NewFileStore(base string) (*FileStore, error) {
	if err := mkDirs(base); err != nil {
		return nil, err
	}
	return &FileStore{base: base}, nil
}

// ----- per-key paths -----

func (fs *FileStore) keysRoot() string {
	return filepath.Join(fs.base, keysDirName)
}

func (fs *FileStore) keyDir(id string) string {
	return filepath.Join(fs.keysRoot(), id)
}

func (fs *FileStore) keyMetaPath(id string) string {
	return filepath.Join(fs.keyDir(id), keyMetaFileName)
}

func (fs *FileStore) keyBinPath(id string) string {
	return filepath.Join(fs.keyDir(id), keyBinFileName)
}

func (fs *FileStore) keyStatePath(id string) string {
	return filepath.Join(fs.keyDir(id), keyStateFileName)
}

// InitMaster creates master.json with Argon2id params & a random salt.
// It is idempotent-safe: returns error if already exists.
func (fs *FileStore) InitMaster() error {
	masterPath := filepath.Join(fs.base, masterFileName)
	if _, err := os.Stat(masterPath); err == nil {
		return ErrMasterJSONAlreadyInitialized
	}
	mf := masterFile{
		Version:                storeFormatVersion,
		Salt:                   randBytes(16),
		Params:                 argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4, KeyLen: 32},
		Created:                time.Now().UTC(),
		NextDeterministicIndex: 1,
	}
	return writeJSONAtomic(masterPath, &mf, 0o600)
}

func (fs *FileStore) nextDeterministicIndex() (uint32, error) {
	fs.masterMu.Lock()
	defer fs.masterMu.Unlock()

	masterPath := filepath.Join(fs.base, masterFileName)
	var mf masterFile
	if err := readJSON(masterPath, &mf); err != nil {
		return 0, err
	}

	if mf.NextDeterministicIndex == 0 {
		ids, err := fs.list()
		if err != nil {
			return 0, err
		}
		mf.NextDeterministicIndex = uint64(len(ids)) + 1
	}

	idx := mf.NextDeterministicIndex
	mf.NextDeterministicIndex++

	if err := writeJSONAtomic(masterPath, &mf, 0o600); err != nil {
		return 0, err
	}

	return uint32(idx), nil
}

// InitInfo returns (master.json present, deterministic flag)
func (fs *FileStore) InitInfo() (masterPresent, deterministic bool, err error) {
	masterPath := filepath.Join(fs.base, masterFileName)
	if _, err := os.Stat(masterPath); err == nil {
		masterPresent = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, false, err
	}

	seedPath := filepath.Join(fs.base, seedFileName)
	f, err := os.Open(seedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return masterPresent, false, nil
		}
		return masterPresent, false, err
	}
	defer f.Close()

	var deterministicByte [1]byte
	n, err := f.Read(deterministicByte[:])
	if err != nil && err != io.EOF {
		return masterPresent, false, err
	}

	if n >= 1 {
		deterministic = deterministicByte[0] == 0x01
	}

	return masterPresent, deterministic, nil
}

func (fs *FileStore) deriveKEK(masterPassword []byte) ([]byte, *masterFile, error) {
	masterPath := filepath.Join(fs.base, masterFileName)
	var mf masterFile
	if err := readJSON(masterPath, &mf); err != nil {
		return nil, nil, err
	}
	params := mf.Params
	kek := argon2.IDKey(masterPassword, mf.Salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	return kek, &mf, nil
}

func (fs *FileStore) readMaster() (*masterFile, error) {
	masterPath := filepath.Join(fs.base, masterFileName)
	var mf masterFile
	if err := readJSON(masterPath, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

func (fs *FileStore) list() ([]string, error) {
	dir := fs.keysRoot()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		id := e.Name()
		if fs.hasKey(id) {
			ids = append(ids, id)
		}
	}

	sort.Strings(ids)

	return ids, nil
}

func (fs *FileStore) createKey(id string, masterPassword []byte, skLE32 []byte, blPubkey, tz4, pop string) error {
	if id == "" {
		return errors.New("id required")
	}

	keyDir := fs.keyDir(id)
	metaPath := fs.keyMetaPath(id)
	binPath := fs.keyBinPath(id)
	if fs.hasKey(id) {
		return ErrKeyExists
	}

	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}

	// derive KEK
	kek, _, err := fs.deriveKEK(masterPassword)
	if err != nil {
		return err
	}
	defer secure.MemoryWipe(kek)

	// random DEK
	dek := randBytes(32)
	defer secure.MemoryWipe(dek)

	// wrap DEK with KEK
	wrapNonce := randBytes(12)
	gcmKEK, err := newAESGCM(kek)
	if err != nil {
		return err
	}
	wrapAAD := []byte("id=" + id + "|tz4=" + tz4)
	wrappedDEK := gcmKEK.Seal(nil, wrapNonce, dek, wrapAAD)

	// enc secret with DEK
	dataNonce := randBytes(12)
	gcmDEK, err := newAESGCM(dek)
	if err != nil {
		return err
	}
	dataAAD := []byte("bl=" + blPubkey + "|tz4=" + tz4)
	encSecret := gcmDEK.Seal(nil, dataNonce, skLE32, dataAAD)

	meta := keyMeta{
		Version:   storeFormatVersion,
		KeyID:     id,
		TZ4:       tz4,
		BLPubkey:  blPubkey,
		Pop:       pop,
		Created:   time.Now().UTC(),
		WrapNonce: wrapNonce,
		DataNonce: dataNonce,
	}
	bundle := keyBundle{
		WrappedDEK: wrappedDEK,
		EncSecret:  encSecret,
	}

	// write files
	if err := writeJSONAtomic(metaPath, &meta, 0o600); err != nil {
		return err
	}

	return writeBytesAtomic(binPath, encodeBundle(bundle), 0o600)
}

func (fs *FileStore) removeKey(id string) error {
	if id == "" {
		return fmt.Errorf("refusing to remove empty key id")
	}
	return os.RemoveAll(fs.keyDir(id))
}

func (fs *FileStore) unlock(id string, masterPassword []byte) (dek []byte, encSecret, dataNonce []byte, blPubkey, tz4 string, err error) {
	var meta keyMeta
	metaPath := fs.keyMetaPath(id)
	binPath := fs.keyBinPath(id)
	if err = readJSON(metaPath, &meta); err != nil {
		return nil, nil, nil, "", "", err
	}
	raw, err := os.ReadFile(binPath)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	bundle, err := decodeBundle(raw)
	if err != nil {
		return nil, nil, nil, "", "", err
	}

	kek, _, err := fs.deriveKEK(masterPassword)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	defer secure.MemoryWipe(kek)

	gcmKEK, err := newAESGCM(kek)
	if err != nil {
		return nil, nil, nil, "", "", err
	}
	dek, err = gcmKEK.Open(nil, meta.WrapNonce, bundle.WrappedDEK, []byte("id="+id+"|tz4="+meta.TZ4))
	if err != nil {
		return nil, nil, nil, "", "", fmt.Errorf("bad password or corrupted key (unwrap)")
	}

	return dek, bundle.EncSecret, meta.DataNonce, meta.BLPubkey, meta.TZ4, nil
}

func (fs *FileStore) readKeyMeta(id string) (keyMeta, error) {
	var m keyMeta
	if err := readJSON(fs.keyMetaPath(id), &m); err != nil {
		return keyMeta{}, err
	}
	return m, nil
}

func (fs *FileStore) hasKey(id string) bool {
	metaPath := fs.keyMetaPath(id)
	_, err := os.Stat(metaPath)
	return err == nil
}

// small binary encoding for keyBundle; you can switch to JSON+base64 if you prefer.
func encodeBundle(b keyBundle) []byte {
	// layout: [u16 len1][len1 bytes][u16 len2][len2 bytes]
	out := make([]byte, 2+len(b.WrappedDEK)+2+len(b.EncSecret))
	off := 0
	putU16 := func(n int) {
		out[off] = byte(n >> 8)
		out[off+1] = byte(n)
		off += 2
	}
	put := func(d []byte) {
		copy(out[off:], d)
		off += len(d)
	}
	putU16(len(b.WrappedDEK))
	put(b.WrappedDEK)
	putU16(len(b.EncSecret))
	put(b.EncSecret)
	return out
}
func decodeBundle(in []byte) (keyBundle, error) {
	getU16 := func(p int) int { return int(in[p])<<8 | int(in[p+1]) }
	if len(in) < 2 {
		return keyBundle{}, fmt.Errorf("short")
	}
	l1 := getU16(0)
	p := 2
	if len(in) < p+l1+2 {
		return keyBundle{}, fmt.Errorf("short")
	}
	w := make([]byte, l1)
	copy(w, in[p:p+l1])
	p += l1
	l2 := getU16(p)
	p += 2
	if len(in) < p+l2 {
		return keyBundle{}, fmt.Errorf("short")
	}
	s := make([]byte, l2)
	copy(s, in[p:p+l2])
	return keyBundle{WrappedDEK: w, EncSecret: s}, nil
}

// WriteSeed stores the seed.bin using the user's KEK derived from their password.
func (fs *FileStore) WriteSeed(masterPassword []byte, enabled bool) error {
	masterPath := filepath.Join(fs.base, masterFileName)
	var mf masterFile
	if err := readJSON(masterPath, &mf); err != nil {
		return err
	}

	kek, _, err := fs.deriveKEK(masterPassword)
	if err != nil {
		return err
	}
	defer secure.MemoryWipe(kek)

	seed := randBytes(32)

	nonce := randBytes(12)
	gcm, err := newAESGCM(kek)
	if err != nil {
		return err
	}
	// bind seed to master.json so it can't be swapped
	aad := make([]byte, 0, 1+len(mf.Salt))
	aad = append(aad, byte(mf.Version))
	aad = append(aad, mf.Salt...)

	ct := gcm.Seal(nil, nonce, seed, aad)

	out := make([]byte, 1+12+len(ct))
	if enabled {
		out[0] = 0x01
	} else {
		out[0] = 0x00
	}
	copy(out[1:], nonce)
	copy(out[1+12:], ct)

	path := filepath.Join(fs.base, seedFileName)
	return writeBytesAtomic(path, out, 0o600)
}

// readSeed loads seed.bin and returns (enabled, seed32).
func (fs *FileStore) readSeed(masterPassword []byte) (bool, []byte, error) {
	path := filepath.Join(fs.base, seedFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return false, nil, err
	}
	if len(b) < 1+12+16 {
		return false, nil, fmt.Errorf("seed file too short")
	}
	enabled := b[0] == 0x01
	nonce := b[1 : 1+12]
	ct := b[1+12:]

	// AAD from master.json
	masterPath := filepath.Join(fs.base, masterFileName)
	var mf masterFile
	if err := readJSON(masterPath, &mf); err != nil {
		return false, nil, err
	}
	aad := make([]byte, 0, 1+len(mf.Salt))
	aad = append(aad, byte(mf.Version))
	aad = append(aad, mf.Salt...)

	kek, _, err := fs.deriveKEK(masterPassword)
	if err != nil {
		return false, nil, err
	}
	defer secure.MemoryWipe(kek)

	gcm, err := newAESGCM(kek)
	if err != nil {
		return false, nil, err
	}
	seed, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return false, nil, fmt.Errorf("seed corrupted or bad password")
	}
	if len(seed) != 32 {
		return false, nil, fmt.Errorf("seed length invalid")
	}
	return enabled, seed, nil
}

func newEmptyKeyState() *KeyState {
	return &KeyState{ByKind: map[int32]*KindState{}}
}

func mergeKeyStates(primary *KeyState, secondary *KeyState) *KeyState {
	merged := newEmptyKeyState()
	for _, kind := range signKinds() {
		var best HighWatermark

		if primary != nil && primary.ByKind != nil {
			if state := primary.ByKind[int32(kind)]; state != nil {
				best = HighWatermark{level: state.GetLevel(), round: state.GetRound()}
			}
		}

		if secondary != nil && secondary.ByKind != nil {
			if state := secondary.ByKind[int32(kind)]; state != nil {
				candidate := HighWatermark{level: state.GetLevel(), round: state.GetRound()}
				if candidate.level > best.level || (candidate.level == best.level && candidate.round > best.round) {
					best = candidate
				}
			}
		}

		merged.ByKind[int32(kind)] = best.ToKeyState()
	}
	return merged
}

func slotIsZero(slot []byte) bool {
	for _, b := range slot {
		if b != 0 {
			return false
		}
	}
	return true
}

func encodeKeyStatePlain(dst []byte, ks *KeyState, seq uint64) {
	for i := range dst {
		dst[i] = 0
	}

	copy(dst[:len(keyStateMagic)], keyStateMagic[:])
	binary.BigEndian.PutUint64(dst[4:12], seq)

	offset := 12
	for _, kind := range signKinds() {
		var state *KindState
		if ks != nil && ks.ByKind != nil {
			state = ks.ByKind[int32(kind)]
		}

		var level uint64
		var round uint32
		if state != nil {
			level = state.GetLevel()
			round = state.GetRound()
		}

		binary.BigEndian.PutUint64(dst[offset:offset+8], level)
		offset += 8
		binary.BigEndian.PutUint32(dst[offset:offset+4], round)
		offset += 4
	}
}

func decodeKeyStatePlain(src []byte) (*KeyState, uint64, error) {
	if len(src) != keyStatePlainSize {
		return nil, 0, fmt.Errorf("%w: invalid plain slot size", ErrKeyStateCorrupted)
	}
	if string(src[:len(keyStateMagic)]) != string(keyStateMagic[:]) {
		return nil, 0, fmt.Errorf("%w: invalid slot header", ErrKeyStateCorrupted)
	}

	seq := binary.BigEndian.Uint64(src[4:12])
	ks := newEmptyKeyState()

	offset := 12
	for _, kind := range signKinds() {
		level := binary.BigEndian.Uint64(src[offset : offset+8])
		offset += 8
		round := binary.BigEndian.Uint32(src[offset : offset+4])
		offset += 4

		ks.ByKind[int32(kind)] = &KindState{
			Level: level,
			Round: round,
		}
	}

	return ks, seq, nil
}

func encodeKeyStateSlot(slot []byte, dek []byte, id, tz4 string, ks *KeyState, seq uint64) error {
	if len(slot) != keyStateSlotSize {
		return fmt.Errorf("invalid slot size %d", len(slot))
	}

	var plain [keyStatePlainSize]byte
	encodeKeyStatePlain(plain[:], ks, seq)
	defer secure.MemoryWipe(plain[:])

	gcm, err := newAESGCM(dek)
	if err != nil {
		return err
	}

	payload := slot[:keyStateSlotDataSize]
	if _, err := io.ReadFull(crypto_rand.Reader, payload[:keyStateNonceSize]); err != nil {
		return err
	}

	aad := []byte("state|id=" + id + "|tz4=" + tz4)
	sealed := gcm.Seal(payload[:keyStateNonceSize], payload[:keyStateNonceSize], plain[:], aad)
	if len(sealed) != keyStateSlotDataSize {
		return fmt.Errorf("invalid sealed slot size %d", len(sealed))
	}

	checksum := crc32.ChecksumIEEE(payload)
	binary.BigEndian.PutUint32(slot[keyStateSlotDataSize:], checksum)
	return nil
}

func decodeKeyStateSlot(slot []byte, dek []byte, id, tz4 string) (*KeyState, uint64, bool, error) {
	if len(slot) != keyStateSlotSize {
		return nil, 0, false, fmt.Errorf("invalid slot size %d", len(slot))
	}
	if slotIsZero(slot) {
		return newEmptyKeyState(), 0, true, nil
	}

	payload := slot[:keyStateSlotDataSize]
	wantChecksum := binary.BigEndian.Uint32(slot[keyStateSlotDataSize:])
	gotChecksum := crc32.ChecksumIEEE(payload)
	if gotChecksum != wantChecksum {
		return nil, 0, false, fmt.Errorf("%w: checksum", ErrKeyStateCorrupted)
	}

	gcm, err := newAESGCM(dek)
	if err != nil {
		return nil, 0, false, err
	}

	var plain [keyStatePlainSize]byte
	defer secure.MemoryWipe(plain[:])

	aad := []byte("state|id=" + id + "|tz4=" + tz4)
	out, err := gcm.Open(plain[:0], payload[:keyStateNonceSize], payload[keyStateNonceSize:], aad)
	if err != nil {
		return nil, 0, false, fmt.Errorf("%w: decrypt", ErrKeyStateCorrupted)
	}

	ks, seq, err := decodeKeyStatePlain(out)
	if err != nil {
		return nil, 0, false, err
	}

	return ks, seq, false, nil
}

func readDoubleBufferKeyState(reader io.ReaderAt, dek []byte, id, tz4 string) (*KeyState, uint64, bool, bool, error) {
	var slotA [keyStateSlotSize]byte
	if _, err := reader.ReadAt(slotA[:], 0); err != nil {
		return nil, 0, false, false, fmt.Errorf("%w: read slot A", ErrKeyStateCorrupted)
	}

	var slotB [keyStateSlotSize]byte
	if _, err := reader.ReadAt(slotB[:], keyStateSlotSize); err != nil {
		return nil, 0, false, false, fmt.Errorf("%w: read slot B", ErrKeyStateCorrupted)
	}

	stateA, seqA, missingA, errA := decodeKeyStateSlot(slotA[:], dek, id, tz4)
	stateB, seqB, missingB, errB := decodeKeyStateSlot(slotB[:], dek, id, tz4)

	missingAll := missingA && missingB
	corrupted := errors.Is(errA, ErrKeyStateCorrupted) || errors.Is(errB, ErrKeyStateCorrupted)

	switch {
	case errA == nil && errB == nil:
		switch {
		case missingAll:
			return newEmptyKeyState(), 0, true, false, nil
		case missingA:
			return stateB, seqB, false, corrupted, nil
		case missingB:
			return stateA, seqA, false, corrupted, nil
		case seqA > seqB:
			return stateA, seqA, false, corrupted, nil
		case seqB > seqA:
			return stateB, seqB, false, corrupted, nil
		default:
			return mergeKeyStates(stateA, stateB), seqA, false, corrupted, nil
		}
	case errA == nil:
		if missingA {
			return newEmptyKeyState(), 0, false, corrupted, nil
		}
		return stateA, seqA, false, corrupted, nil
	case errB == nil:
		if missingB {
			return newEmptyKeyState(), 0, false, corrupted, nil
		}
		return stateB, seqB, false, corrupted, nil
	default:
		return nil, 0, missingAll, corrupted, errA
	}
}

func readLegacyKeyStateFile(path string, dek []byte, id, tz4 string) (*KeyState, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newEmptyKeyState(), true, nil
		}
		return nil, false, err
	}
	if len(b) < 12+16 {
		return nil, false, fmt.Errorf("%w: file too short", ErrKeyStateCorrupted)
	}
	nonce := b[:12]
	ct := b[12:]

	gcm, err := newAESGCM(dek)
	if err != nil {
		return nil, false, err
	}
	aad := []byte("state|id=" + id + "|tz4=" + tz4)

	plain, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, false, fmt.Errorf("%w: decrypt", ErrKeyStateCorrupted)
	}
	var ks KeyState
	if err := proto.Unmarshal(plain, &ks); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrKeyStateCorrupted, err)
	}
	if ks.ByKind == nil {
		ks.ByKind = map[int32]*KindState{}
	}
	return &ks, false, nil
}

func (fs *FileStore) readLegacyKeyState(id string, dek []byte, tz4 string) (*KeyState, uint64, bool, bool, error) {
	path := fs.keyStatePath(id)
	backupPath := path + tmpSuffix

	keyState, missing, err := readLegacyKeyStateFile(path, dek, id, tz4)
	backupKeyState, backupMissing, backupErr := readLegacyKeyStateFile(backupPath, dek, id, tz4)

	missingAll := missing && backupMissing
	corrupted := errors.Is(err, ErrKeyStateCorrupted) || errors.Is(backupErr, ErrKeyStateCorrupted)

	switch {
	case err == nil && backupErr == nil:
		return mergeKeyStates(keyState, backupKeyState), 0, missingAll, corrupted, nil
	case err == nil:
		return keyState, 0, missingAll, corrupted, nil
	case backupErr == nil:
		return backupKeyState, 0, missingAll, corrupted, nil
	default:
		return nil, 0, missingAll, corrupted, err
	}
}

// readKeyState loads level.bin with DEK. If missing, returns zero-initialized state.
// It also reports whether any of the backing files failed integrity checks.
func (fs *FileStore) readKeyState(id string, dek []byte, tz4 string) (*KeyState, uint64, bool, bool, keyStateFormat, error) {
	if len(dek) != 32 {
		return nil, 0, false, false, keyStateFormatMissing, fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}
	path := fs.keyStatePath(id)

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		keyState, seq, missing, corrupted, readErr := fs.readLegacyKeyState(id, dek, tz4)
		return keyState, seq, missing, corrupted, keyStateFormatMissing, readErr
	case err != nil:
		return nil, 0, false, false, keyStateFormatMissing, err
	case info.Size() == keyStateFileSize:
		file, err := os.Open(path)
		if err != nil {
			return nil, 0, false, false, keyStateFormatDoubleBuffer, err
		}
		defer file.Close()

		keyState, seq, missing, corrupted, readErr := readDoubleBufferKeyState(file, dek, id, tz4)
		return keyState, seq, missing, corrupted, keyStateFormatDoubleBuffer, readErr
	default:
		keyState, seq, missing, corrupted, readErr := fs.readLegacyKeyState(id, dek, tz4)
		return keyState, seq, missing, corrupted, keyStateFormatLegacy, readErr
	}
}

func (fs *FileStore) prepareKeyStateFile(id string, dek []byte, tz4 string) (*os.File, *KeyState, uint64, bool, bool, error) {
	ks, seq, missing, corrupted, format, err := fs.readKeyState(id, dek, tz4)
	if err != nil {
		return nil, nil, 0, missing, corrupted, err
	}

	path := fs.keyStatePath(id)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_DSYNC, 0o600)
	if err != nil {
		return nil, nil, 0, missing, corrupted, err
	}

	needsInit := format != keyStateFormatDoubleBuffer
	if !needsInit {
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, nil, 0, missing, corrupted, err
		}
		needsInit = info.Size() != keyStateFileSize
	}

	if !needsInit {
		return file, ks, seq, missing, corrupted, nil
	}

	if err := file.Truncate(keyStateFileSize); err != nil {
		file.Close()
		return nil, nil, 0, missing, corrupted, err
	}

	if missing {
		return file, ks, 0, true, corrupted, nil
	}

	if seq == 0 {
		seq = 1
	}
	if err := fs.writeKeyState(file, id, dek, tz4, ks, seq); err != nil {
		file.Close()
		return nil, nil, 0, missing, corrupted, err
	}

	return file, ks, seq, missing, corrupted, nil
}

func (fs *FileStore) readKeyStateFromFile(file *os.File, id string, dek []byte, tz4 string) (*KeyState, uint64, bool, bool, error) {
	if len(dek) != 32 {
		return nil, 0, false, false, fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}
	return readDoubleBufferKeyState(file, dek, id, tz4)
}

func (fs *FileStore) writeKeyState(file *os.File, id string, dek []byte, tz4 string, ks *KeyState, seq uint64) error {
	if file == nil {
		return fmt.Errorf("state file is not open")
	}
	if len(dek) != 32 {
		return fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}

	var slot [keyStateSlotSize]byte
	if err := encodeKeyStateSlot(slot[:], dek, id, tz4, ks, seq); err != nil {
		return err
	}

	if _, err := file.WriteAt(slot[:], 0); err != nil {
		return err
	}

	if _, err := file.WriteAt(slot[:], keyStateSlotSize); err != nil {
		return err
	}
	return nil
}
