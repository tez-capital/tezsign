package secure

import (
	crypto_rand "crypto/rand"
	"fmt"
	"io"
	"math/big"
)

const (
	obfuscatedKeyMinBufferSize = 800
	obfuscatedKeySizeJitter    = 400
	obfuscatedKeyLength        = 32
)

// ObfuscatedState stores a 32-byte key split across two random-sized haystacks.
// One buffer contains key bytes XORed with a pad; the other contains the pad.
type ObfuscatedState struct {
	bufferA []byte
	bufferB []byte
	headA   uintptr // offset into bufferA; not a raw memory address
	headB   uintptr // offset into bufferB; not a raw memory address
}

func NewObfuscatedKey(key [obfuscatedKeyLength]byte) (*ObfuscatedState, error) {
	sizeA, err := randomBufferSize()
	if err != nil {
		return nil, err
	}
	sizeB, err := randomBufferSize()
	if err != nil {
		return nil, err
	}

	k := &ObfuscatedState{
		bufferA: make([]byte, sizeA),
		bufferB: make([]byte, sizeB),
	}
	if _, err := io.ReadFull(crypto_rand.Reader, k.bufferA); err != nil {
		k.Clear()
		return nil, err
	}
	if _, err := io.ReadFull(crypto_rand.Reader, k.bufferB); err != nil {
		k.Clear()
		return nil, err
	}

	headA, err := randomOffset(sizeA)
	if err != nil {
		k.Clear()
		return nil, err
	}
	headB, err := randomOffset(sizeB)
	if err != nil {
		k.Clear()
		return nil, err
	}

	k.headA = headA
	k.headB = headB
	for i := range key {
		k.bufferA[int(k.headA)+i] = k.bufferB[int(k.headB)+i] ^ key[i]
	}

	return k, nil
}

func randomBufferSize() (int, error) {
	n, err := randomInt(obfuscatedKeySizeJitter)
	if err != nil {
		return 0, err
	}
	return obfuscatedKeyMinBufferSize + n, nil
}

func randomOffset(bufferSize int) (uintptr, error) {
	limit := bufferSize - obfuscatedKeyLength + 1
	n, err := randomInt(limit)
	if err != nil {
		return 0, err
	}
	return uintptr(n), nil
}

func randomInt(limit int) (int, error) {
	n, err := crypto_rand.Int(crypto_rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

func (k *ObfuscatedState) WithPlaintext(fn func(*[obfuscatedKeyLength]byte) error) error {
	if k == nil {
		return fmt.Errorf("missing obfuscated key")
	}

	var plain [obfuscatedKeyLength]byte
	defer MemoryWipe(plain[:])
	for i := range plain {
		plain[i] = k.bufferA[int(k.headA)+i] ^ k.bufferB[int(k.headB)+i]
	}

	return fn(&plain)
}

func (k *ObfuscatedState) Clear() {
	if k == nil {
		return
	}
	MemoryWipe(k.bufferA)
	MemoryWipe(k.bufferB)
	k.bufferA = nil
	k.bufferB = nil
	k.headA = 0
	k.headB = 0
}
