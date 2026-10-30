package pii_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/bbxx111/accountkit/pii"
)

// 用标准库独立构造 nonce||ciphertext 与 HMAC-SHA256 十六进制摘要，
// 钉住存储格式；轮换到新活跃版本后旧版本数据仍须可解密、可匹配。
func TestStoredFormatsSurviveKeyRotation(t *testing.T) {
	const plain = "+8613812341234"
	for _, version := range []uint16{1, 2} {
		k := keys()[version]

		block, err := aes.NewCipher(k)
		if err != nil {
			t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		nonce := bytesOf(0x42, gcm.NonceSize())
		handBuilt := gcm.Seal(append([]byte(nil), nonce...), nonce, []byte(plain), nil)

		mac := hmac.New(sha256.New, k)
		mac.Write([]byte(plain))
		handDigest := hex.EncodeToString(mac.Sum(nil))

		// 轮换后：活跃版本为 2，1 仍配置。
		c, err := pii.NewCipher(keys(), 2)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Decrypt(handBuilt, version)
		if err != nil || got != plain {
			t.Fatalf("v%d hand-built nonce||ciphertext: %q %v", version, got, err)
		}
		d, err := pii.NewDigester(keys(), 2)
		if err != nil {
			t.Fatal(err)
		}
		dv, err := d.DigestFor(plain, version)
		if err != nil || dv != handDigest {
			t.Fatalf("v%d digest: %q want %q (%v)", version, dv, handDigest, err)
		}
		found := false
		for _, a := range d.AllDigests(plain) {
			found = found || a == handDigest
		}
		if !found {
			t.Fatalf("v%d digest must be matchable via AllDigests after rotation", version)
		}
	}

	// 当前实现产出的密文同样是 nonce||ciphertext。
	c, _ := pii.NewCipher(keys(), 1)
	ct, _, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(keys()[1])
	gcm, _ := cipher.NewGCM(block)
	if out, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], nil); err != nil || string(out) != plain {
		t.Fatalf("Encrypt output must be nonce||ciphertext: %q %v", out, err)
	}
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
