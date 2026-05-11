package secure

import (
	crypto_rand "crypto/rand"
	"fmt"
	"io"
	"math/big"
)

const obfuscatedKeyBufferSize = 1024

// ObfuscatedKey stores short key material split across two random 1 KiB buffers.
// One buffer contains key bytes XORed with a pad; the other contains the pad.
type ObfuscatedKey struct {
	obfuscated [obfuscatedKeyBufferSize]byte
	xorPad     [obfuscatedKeyBufferSize]byte
	keyOffset  int
	padOffset  int
	length     int
}

func NewObfuscatedKey(key []byte) (*ObfuscatedKey, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("empty key")
	}
	if len(key) > obfuscatedKeyBufferSize {
		return nil, fmt.Errorf("key too large for obfuscated storage")
	}

	k := &ObfuscatedKey{length: len(key)}
	if _, err := io.ReadFull(crypto_rand.Reader, k.obfuscated[:]); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(crypto_rand.Reader, k.xorPad[:]); err != nil {
		k.Clear()
		return nil, err
	}

	keyOffset, err := randomOffset(len(key))
	if err != nil {
		k.Clear()
		return nil, err
	}
	padOffset, err := randomOffset(len(key))
	if err != nil {
		k.Clear()
		return nil, err
	}

	k.keyOffset = keyOffset
	k.padOffset = padOffset
	for i := range key {
		k.obfuscated[k.keyOffset+i] = key[i] ^ k.xorPad[k.padOffset+i]
	}

	return k, nil
}

func randomOffset(length int) (int, error) {
	limit := obfuscatedKeyBufferSize - length + 1
	n, err := crypto_rand.Int(crypto_rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()), nil
}

func (k *ObfuscatedKey) WithPlaintext(fn func([]byte) error) error {
	if k == nil {
		return fmt.Errorf("missing obfuscated key")
	}

	plain := make([]byte, k.length)
	defer MemoryWipe(plain)
	for i := range plain {
		plain[i] = k.obfuscated[k.keyOffset+i] ^ k.xorPad[k.padOffset+i]
	}

	return fn(plain)
}

func (k *ObfuscatedKey) Clear() {
	if k == nil {
		return
	}
	MemoryWipe(k.obfuscated[:])
	MemoryWipe(k.xorPad[:])
	k.keyOffset = 0
	k.padOffset = 0
	k.length = 0
}
