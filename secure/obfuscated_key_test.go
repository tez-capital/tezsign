package secure

import (
	"bytes"
	"testing"
)

func TestObfuscatedKeyWithPlaintext(t *testing.T) {
	key := [32]byte([]byte("0123456789abcdef0123456789abcdef"))

	obfuscated, err := NewObfuscatedKey(key)
	if err != nil {
		t.Fatalf("NewObfuscatedKey: %v", err)
	}
	defer obfuscated.Clear()

	if err := obfuscated.WithPlaintext(func(plain *[32]byte) error {
		if !bytes.Equal(plain[:], key[:]) {
			t.Fatalf("plain mismatch")
		}
		return nil
	}); err != nil {
		t.Fatalf("WithPlaintext: %v", err)
	}
}

func TestObfuscatedKeyClear(t *testing.T) {
	key := [32]byte([]byte("0123456789abcdef0123456789abcdef"))

	obfuscated, err := NewObfuscatedKey(key)
	if err != nil {
		t.Fatalf("NewObfuscatedKey: %v", err)
	}

	bufferA := obfuscated.bufferA
	bufferB := obfuscated.bufferB
	obfuscated.Clear()

	if !allZero(bufferA) {
		t.Fatalf("bufferA not cleared")
	}
	if !allZero(bufferB) {
		t.Fatalf("bufferB not cleared")
	}
	if obfuscated.bufferA != nil {
		t.Fatalf("bufferA reference not cleared")
	}
	if obfuscated.bufferB != nil {
		t.Fatalf("bufferB reference not cleared")
	}
	if obfuscated.headA != 0 || obfuscated.headB != 0 {
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
