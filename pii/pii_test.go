package pii_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/pii"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func keys() map[uint16][]byte { return map[uint16][]byte{1: key(1), 2: key(2)} }

func TestParseKeyList(t *testing.T) {
	spec := "1:" + base64.StdEncoding.EncodeToString(key(1)) + ",2:" + base64.StdEncoding.EncodeToString(key(2))
	got, err := pii.ParseKeyList(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !bytes.Equal(got[1], key(1)) || !bytes.Equal(got[2], key(2)) {
		t.Fatalf("got %v", got)
	}
}

func TestParseKeyListRejects(t *testing.T) {
	short := "1:" + base64.StdEncoding.EncodeToString(key(1)[:16])
	dup := "1:" + base64.StdEncoding.EncodeToString(key(1)) + ",1:" + base64.StdEncoding.EncodeToString(key(2))
	for name, spec := range map[string]string{
		"empty": "", "no colon": "abc", "bad version": "x:" + base64.StdEncoding.EncodeToString(key(1)),
		"zero version": "0:" + base64.StdEncoding.EncodeToString(key(1)), "bad base64": "1:!!!",
		"short key": short, "duplicate version": dup,
	} {
		if _, err := pii.ParseKeyList(spec); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseKeyListErrorDoesNotEchoSecret(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(key(9))
	_, err := pii.ParseKeyList(secret) // 缺 version:
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret[:16]) {
		t.Fatalf("error must not echo key material: %v", err)
	}
}

func TestCipherRoundTripAndVersion(t *testing.T) {
	c, err := pii.NewCipher(keys(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ct, v, err := c.Encrypt("+8613812341234")
	if err != nil || v != 2 {
		t.Fatalf("encrypt: v=%d err=%v", v, err)
	}
	plain, err := c.Decrypt(ct, 2)
	if err != nil || plain != "+8613812341234" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := c.Decrypt(ct, 1); err == nil {
		t.Fatal("decrypt with the wrong version must fail, not fall back")
	}
	if _, err := c.Decrypt(ct, 9); err == nil {
		t.Fatal("unknown version must fail")
	}
	if _, err := c.Decrypt(ct[:5], 2); err == nil {
		t.Fatal("ciphertext shorter than nonce must fail")
	}
	ct2, _, _ := c.Encrypt("+8613812341234")
	if bytes.Equal(ct, ct2) {
		t.Fatal("encryption must be randomized (fresh nonce)")
	}
	if c.Active() != 2 || len(c.Versions()) != 2 || c.Versions()[0] != 1 {
		t.Fatalf("Active/Versions: %d %v", c.Active(), c.Versions())
	}
}

func TestNewCipherAndDigesterValidate(t *testing.T) {
	if _, err := pii.NewCipher(nil, 1); err == nil {
		t.Fatal("no keys must fail")
	}
	if _, err := pii.NewCipher(keys(), 3); err == nil {
		t.Fatal("active version not in keys must fail")
	}
	if _, err := pii.NewCipher(map[uint16][]byte{1: key(1)[:31]}, 1); err == nil {
		t.Fatal("short key must fail")
	}
	if _, err := pii.NewDigester(keys(), 3); err == nil {
		t.Fatal("digester active version not in keys must fail")
	}
}

func TestDigesterIsKeyedStableAndVersioned(t *testing.T) {
	d, err := pii.NewDigester(keys(), 2)
	if err != nil {
		t.Fatal(err)
	}
	d1, v := d.Digest("a@x.com")
	d2, _ := d.Digest("a@x.com")
	if d1 != d2 || v != 2 || len(d1) != 64 || strings.ToLower(d1) != d1 {
		t.Fatalf("digest: %q v=%d", d1, v)
	}
	old, err := d.DigestFor("a@x.com", 1)
	if err != nil || old == d1 {
		t.Fatalf("DigestFor(1) must differ from active digest: %q %v", old, err)
	}
	if _, err := d.DigestFor("a@x.com", 9); err == nil {
		t.Fatal("unknown version must fail")
	}
	all := d.AllDigests("a@x.com")
	if len(all) != 2 || all[0] != d1 || all[1] != old {
		t.Fatalf("AllDigests order must be active first then ascending: %v", all)
	}
	// 密钥拷贝：调用方事后篡改自己的 map 不得影响摘要
	k := keys()
	d3, _ := pii.NewDigester(k, 2)
	before, _ := d3.Digest("x")
	k[2][0] ^= 0xff
	after, _ := d3.Digest("x")
	if before != after {
		t.Fatal("digester must copy caller keys")
	}
}

func TestNewCipherRequiresExactly32ByteKeys(t *testing.T) {
	long := bytes.Repeat([]byte{7}, 64)
	if _, err := pii.NewCipher(map[uint16][]byte{1: long}, 1); err == nil || !strings.Contains(err.Error(), "exactly 32") {
		t.Fatalf("64-byte cipher key must be rejected explicitly, got %v", err)
	}
	// Digester 接受 ≥ 32 字节的密钥（HMAC 使用全部密钥材料）
	if _, err := pii.NewDigester(map[uint16][]byte{1: long}, 1); err != nil {
		t.Fatalf("64-byte digester key must be accepted: %v", err)
	}
}
