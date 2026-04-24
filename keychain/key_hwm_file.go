package keychain

import (
	crypto_rand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"syscall"

	"github.com/tez-capital/tezsign/secure"
)

const (
	keyStateSlotSize     = 100
	keyStateSlotDataSize = keyStateSlotSize - 4
	keyStateFileSize     = 2 * keyStateSlotSize
	keyStateNonceSize    = 12
	keyStatePlainSize    = 68
)

var keyStateMagic = [4]byte{'H', 'W', 'M', '1'}

func newEmptyKeyState() *KeyState {
	return &KeyState{ByKind: map[int32]*KindState{}}
}

func newZeroKeyState() *KeyState {
	ks := newEmptyKeyState()
	for _, kind := range allSignKinds {
		ks.ByKind[int32(kind)] = &KindState{}
	}
	return ks
}

func ensureKeyState(ks *KeyState) *KeyState {
	if ks == nil || len(ks.ByKind) == 0 {
		return newZeroKeyState()
	}
	return ks
}

func mergeKeyStates(primary *KeyState, secondary *KeyState) *KeyState {
	merged := newEmptyKeyState()
	for _, kind := range allSignKinds {
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

func statePlusOne(base *KeyState) *KeyState {
	newState := newEmptyKeyState()

	for _, kind := range allSignKinds {
		var state *KindState
		if base != nil && base.ByKind != nil {
			state = base.ByKind[int32(kind)]
		}

		var level uint64
		if state != nil {
			level = state.GetLevel()
		}

		newState.ByKind[int32(kind)] = &KindState{
			Level: level + 1,
			Round: 0,
		}
	}

	return newState
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
	for _, kind := range allSignKinds {
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
	for _, kind := range allSignKinds {
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
		return newZeroKeyState(), 0, true, nil
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

func writeMirroredKeyState(file *os.File, dek []byte, id, tz4 string, ks *KeyState, seq uint64) error {
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
			return newZeroKeyState(), 0, true, false, nil
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
			return newZeroKeyState(), 0, false, corrupted, nil
		}
		return statePlusOne(stateA), seqA, false, corrupted, nil
	case errB == nil:
		if missingB {
			return newZeroKeyState(), 0, false, corrupted, nil
		}
		return statePlusOne(stateB), seqB, false, corrupted, nil
	default:
		return nil, 0, missingAll, corrupted, errA
	}
}

func readKeyHWMState(path string, dek []byte, id, tz4 string) (*KeyState, uint64, bool, bool, error) {
	if len(dek) != 32 {
		return nil, 0, false, false, fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}
	file, err := os.Open(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return newZeroKeyState(), 0, true, false, nil
		default:
			return nil, 0, false, false, err
		}
	}
	defer file.Close()

	return readDoubleBufferKeyState(file, dek, id, tz4)
}

func openKeyHWMFile(path string, dek []byte, id, tz4 string) (*keyHWMFile, *KeyState, uint64, bool, error) {
	ks, seq, missing, corrupted, err := readKeyHWMState(path, dek, id, tz4)
	if err != nil {
		return nil, nil, 0, corrupted, err
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_SYNC, 0o600)
	if err != nil {
		return nil, nil, 0, corrupted, err
	}

	fail := func(e error) (*keyHWMFile, *KeyState, uint64, bool, error) {
		file.Close()
		return nil, nil, 0, corrupted, e
	}
	finish := func(nextSlot int, outKS *KeyState, outSeq uint64) (*keyHWMFile, *KeyState, uint64, bool, error) {
		return newKeyHWMFile(file, nextSlot), outKS, outSeq, corrupted, nil
	}

	needsInit := false
	if !missing {
		info, err := file.Stat()
		if err != nil {
			return fail(err)
		}
		needsInit = info.Size() != keyStateFileSize
	} else {
		needsInit = true
	}

	if !needsInit {
		nextSlot := chooseNextKeyHWMFileSlot(file, dek, id, tz4)
		return finish(nextSlot, ks, seq)
	}

	if err := file.Truncate(keyStateFileSize); err != nil {
		return fail(err)
	}

	ks = ensureKeyState(ks)
	if missing {
		if err := writeMirroredKeyState(file, dek, id, tz4, ks, 0); err != nil {
			return fail(err)
		}
		if err := file.Sync(); err != nil {
			return fail(err)
		}
		if err := syncParentDir(path); err != nil {
			return fail(err)
		}
		return finish(0, ks, 0)
	}

	if seq == 0 {
		seq = 1
	}
	if err := writeMirroredKeyState(file, dek, id, tz4, ks, seq); err != nil {
		return fail(err)
	}
	if err := syncParentDir(path); err != nil {
		return fail(err)
	}

	return finish(0, ks, seq)
}

type keyHWMWriteRequest struct {
	dek []byte
	id  string
	tz4 string
	ks  *KeyState
	seq uint64
}

// keyHWMFile wraps the on-disk double-buffered high-watermark file.
// The worker owns the next slot index and performs the primary persisted
// write per request before accepting the next one.
type keyHWMFile struct {
	file    *os.File
	workCh  chan keyHWMWriteRequest
	syncCh  chan struct{}
	respCh  chan error
	stopped chan struct{}
}

func newKeyHWMFile(file *os.File, nextSlot int) *keyHWMFile {
	hwmFile := &keyHWMFile{
		file:    file,
		workCh:  make(chan keyHWMWriteRequest),
		syncCh:  make(chan struct{}),
		respCh:  make(chan error, 1),
		stopped: make(chan struct{}),
	}
	go hwmFile.worker(nextSlot % 2)
	return hwmFile
}

func (file *keyHWMFile) worker(nextSlot int) {
	defer close(file.stopped)
	for {
		select {
		case req, ok := <-file.workCh:
			if !ok {
				return
			}
			var slot [keyStateSlotSize]byte
			if err := encodeKeyStateSlot(slot[:], req.dek, req.id, req.tz4, req.ks, req.seq); err != nil {
				file.respCh <- err
				continue
			}
			offset := int64(nextSlot) * keyStateSlotSize
			_, err := file.file.WriteAt(slot[:], offset)
			file.respCh <- err
			nextSlot ^= 1
		case <-file.syncCh:
			file.respCh <- nil
		}
	}
}

func chooseNextKeyHWMFileSlot(reader io.ReaderAt, dek []byte, id, tz4 string) int {
	var slotA [keyStateSlotSize]byte
	if _, err := reader.ReadAt(slotA[:], 0); err != nil {
		return 0
	}
	var slotB [keyStateSlotSize]byte
	if _, err := reader.ReadAt(slotB[:], keyStateSlotSize); err != nil {
		return 1
	}

	_, seqA, missingA, errA := decodeKeyStateSlot(slotA[:], dek, id, tz4)
	_, seqB, missingB, errB := decodeKeyStateSlot(slotB[:], dek, id, tz4)

	aBad := errA != nil || missingA
	bBad := errB != nil || missingB

	switch {
	case aBad && !bBad:
		return 0
	case bBad && !aBad:
		return 1
	default:
		if seqA <= seqB {
			return 0
		}
		return 1
	}
}

func (file *keyHWMFile) persistAsync(dek []byte, id, tz4 string, ks *KeyState, seq uint64) {
	file.workCh <- keyHWMWriteRequest{dek: dek, id: id, tz4: tz4, ks: ks, seq: seq}
}

func (file *keyHWMFile) waitPersist() error {
	return <-file.respCh
}

func (file *keyHWMFile) persist(dek []byte, id, tz4 string, ks *KeyState, seq uint64) error {
	file.persistAsync(dek, id, tz4, ks, seq)
	return file.waitPersist()
}

func (file *keyHWMFile) load(dek []byte, id, tz4 string) (*KeyState, uint64, bool, bool, error) {
	if len(dek) != 32 {
		return nil, 0, false, false, fmt.Errorf("invalid DEK (len=%d)", len(dek))
	}
	return readDoubleBufferKeyState(file.file, dek, id, tz4)
}

func (file *keyHWMFile) waitIdle() {
	file.syncCh <- struct{}{}
	<-file.respCh
}

func (file *keyHWMFile) Close() error {
	close(file.workCh)
	<-file.stopped
	return file.file.Close()
}
