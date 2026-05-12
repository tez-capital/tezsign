package secure

import (
	"bytes"
	"testing"
)

func TestObfuscatedKeyWithPlaintext(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	obfuscated, err := NewObfuscatedKey(key)
	if err != nil {
		t.Fatalf("NewObfuscatedKey: %v", err)
	}
	defer obfuscated.Clear()

	if err := obfuscated.WithPlaintext(func(plain []byte) error {
		if !bytes.Equal(plain, key) {
			t.Fatalf("plain mismatch")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithPlaintext: %v", err)
	}
}

func TestObfuscatedKeyClear(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	obfuscated, err := NewObfuscatedKey(key)
	if err != nil {
		t.Fatalf("NewObfuscatedKey: %v", err)
	}

	obfuscated.Clear()

	if !allZero(obfuscated.obfuscated[:]) {
		t.Fatalf("obfuscated buffer not cleared")
	}
	if !allZero(obfuscated.xorPad[:]) {
		t.Fatalf("xor pad buffer not cleared")
	}
	if obfuscated.keyOffset != 0 || obfuscated.padOffset != 0 {
		t.Fatalf("metadata not cleared")
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
