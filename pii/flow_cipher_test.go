package pii

import (
	"bytes"
	"testing"
)

func TestCipherAADFlowIsolation(t *testing.T) {
	c, err := NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ct, v, err := c.EncryptWithAAD("proof", []byte("instance:a:flow:1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.DecryptWithAAD(ct, v, []byte("instance:a:flow:2")); err == nil {
		t.Fatal("cross flow accepted")
	}
	if _, err = c.Decrypt(ct, v); err == nil {
		t.Fatal("unbound accepted")
	}
	if got, err := c.DecryptWithAAD(ct, v, []byte("instance:a:flow:1")); err != nil || got != "proof" {
		t.Fatalf("roundtrip %q %v", got, err)
	}
}
