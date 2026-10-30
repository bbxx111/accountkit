package user

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// 钉住 refresh token 的存储格式：库中只存明文的 SHA-256 原始 32 字节。
func TestRefreshTokenHashFormat(t *testing.T) {
	for _, plain := range []string{"refresh-token-a", "another_refresh-token.B"} {
		want := sha256.Sum256([]byte(plain))
		if got := hashRefresh(plain); !bytes.Equal(got, want[:]) {
			t.Fatalf("hashRefresh(%q) = %x, want %x", plain, got, want)
		}
	}
	plain, hash, err := newRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(plain)
	if err != nil || len(raw) != 32 {
		t.Fatalf("plain must be base64url of 32 bytes: len=%d err=%v", len(raw), err)
	}
	want := sha256.Sum256([]byte(plain))
	if !bytes.Equal(hash, want[:]) {
		t.Fatalf("returned hash %x != sha256(plain) %x", hash, want)
	}
}
