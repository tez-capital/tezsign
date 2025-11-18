package keychain

import (
	"crypto/aes"
	"crypto/cipher"
	crypto_rand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	unsafe "unsafe"

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
)

var (
	ErrKeyExists                    = errors.New("key_id already exists")
	ErrMasterJSONAlreadyInitialized = errors.New("master json already initialized")
	ErrKeyStateCorrupted            = errors.New("state corrupted")
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

//go:linkname memclrNoHeapPointers runtime.memclrNoHeapPointers
//go:noescape
func memclrNoHeapPointers(ptr unsafe.Pointer, len uintptr)

// MemoryWipe is the fastest, most secure way to zero a byte slice.
// It uses the Go runtime's internal, highly-optimized, non-optimizable
// memory clear function.
func MemoryWipe(b []byte) {
	if len(b) == 0 {
		return
	}

	memclrNoHeapPointers(unsafe.Pointer(&b[0]), uintptr(len(b)))
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
	defer MemoryWipe(kek)

	// random DEK
	dek := randBytes(32)
	defer MemoryWipe(dek)

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
	defer MemoryWipe(kek)

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
	defer MemoryWipe(kek)

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
	defer MemoryWipe(kek)

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

func readKeyStateFile(path string, dek []byte, id, tz4 string) (*KeyState, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &KeyState{ByKind: map[int32]*KindState{}}, true, nil
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

// readKeyState loads level.bin with DEK. If missing, returns zero-initialized state.
// It also reports whether any of the backing files failed integrity checks.
func (fs *FileStore) readKeyState(id string, dek []byte, tz4 string) (*KeyState, bool, bool, error) {
	if len(dek) != 32 {
		return nil, false, false, fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}
	path := fs.keyStatePath(id)

	backupPath := path + tmpSuffix
	keyState, missing, err := readKeyStateFile(path, dek, id, tz4)
	backupKeyState, backupMissing, backupErr := readKeyStateFile(backupPath, dek, id, tz4)

	missingAll := missing && backupMissing
	corrupted := errors.Is(err, ErrKeyStateCorrupted) || errors.Is(backupErr, ErrKeyStateCorrupted)

	switch {
	case err == nil && backupErr == nil:
		for k, v := range backupKeyState.ByKind {
			existing, ok := keyState.ByKind[k]
			if !ok || v.GetLevel() > existing.GetLevel() {
				keyState.ByKind[k] = v
			}
		}
		return keyState, missingAll, corrupted, nil
	case err == nil:
		return keyState, missingAll, corrupted, nil
	case backupErr == nil:
		return backupKeyState, missingAll, corrupted, nil
	default:
		// both files failed with hard errors (not handled as "missing")
		return nil, missingAll, corrupted, err
	}
}

func (fs *FileStore) writeKeyState(id string, dek []byte, tz4 string, ks *KeyState) error {
	path := fs.keyStatePath(id)

	plain, err := proto.Marshal(ks)
	if err != nil {
		return err
	}
	nonce := randBytes(12)

	gcm, err := newAESGCM(dek)
	if err != nil {
		return err
	}
	aad := []byte("state|id=" + id + "|tz4=" + tz4)
	ct := gcm.Seal(nil, nonce, plain, aad)

	out := make([]byte, 12+len(ct))
	copy(out[:12], nonce)
	copy(out[12:], ct)

	err = writeBytesAtomic(path, out, 0o600)
	return err
}
