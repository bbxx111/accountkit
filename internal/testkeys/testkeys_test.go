package testkeys_test

import (
	"crypto/elliptic"
	"crypto/sha256"
	"testing"

	"github.com/bbxx111/accountkit/internal/testkeys"
)

// 固定期望值确保密钥跨进程确定（多进程恢复演练依赖这一点）。
func TestJWTDeterministicAndDistinct(t *testing.T) {
	a, b := testkeys.JWT(1), testkeys.JWT(2)
	if a.Curve != elliptic.P256() || a.Equal(b) {
		t.Fatal("keys must be distinct P-256 keys")
	}
	if got := testkeys.JWTBase64(1); got != testkeys.JWTBase64(1) || len(got) == 0 {
		t.Fatal("base64 encoding unstable")
	}
	raw, err := a.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// 标量即种子摘要本身，与进程状态无关。
	if want := sha256.Sum256([]byte("accountkit-synthetic-test-jwt-key-1")); string(raw) != string(want[:]) {
		t.Fatalf("JWT(1) changed across processes: %x", raw)
	}
}
