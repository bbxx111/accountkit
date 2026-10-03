package idp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
)

func rsaJWKFixture(kid string, pub *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func mustJWKSJSON(t *testing.T, keys ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRSAJWKSetValid(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := parseRSAJWKSet(mustJWKSJSON(t, rsaJWKFixture("k1", &key.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := keys["k1"]
	if !ok || got.N.Cmp(key.PublicKey.N) != 0 || got.E != key.PublicKey.E {
		t.Fatalf("parsed key mismatch: %+v", got)
	}
}

func TestParseRSAJWKSetSkipsNonRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ec := map[string]any{"kty": "EC", "kid": "ec1", "crv": "P-256", "x": "x", "y": "y"}
	keys, err := parseRSAJWKSet(mustJWKSJSON(t, ec, rsaJWKFixture("k1", &key.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["ec1"]; ok {
		t.Fatal("EC key must be skipped, not parsed as RSA")
	}
	if _, ok := keys["k1"]; !ok {
		t.Fatal("RSA key must be kept")
	}
}

func TestParseRSAJWKSetNoRSAKeysIsError(t *testing.T) {
	ec := map[string]any{"kty": "EC", "kid": "ec1", "crv": "P-256", "x": "x", "y": "y"}
	if _, err := parseRSAJWKSet(mustJWKSJSON(t, ec)); err == nil {
		t.Fatal("expected error when the set has no RSA keys")
	}
}

func TestParseRSAJWKSetUndecodableModulusIsError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	bad := rsaJWKFixture("k1", &key.PublicKey)
	bad["n"] = "not-valid-base64url!!"
	if _, err := parseRSAJWKSet(mustJWKSJSON(t, bad)); err == nil {
		t.Fatal("expected error for undecodable n")
	}
}

func TestParseRSAJWKSetSmallModulusIsError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024) // < 2048 bit
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRSAJWKSet(mustJWKSJSON(t, rsaJWKFixture("k1", &key.PublicKey))); err == nil {
		t.Fatal("expected error for modulus < 2048 bits")
	}
}

func TestParseRSAJWKSetBadExponentIsError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	bad := rsaJWKFixture("k1", &key.PublicKey)
	bad["e"] = base64.RawURLEncoding.EncodeToString(big.NewInt(1).Bytes()) // exponent < 3
	if _, err := parseRSAJWKSet(mustJWKSJSON(t, bad)); err == nil {
		t.Fatal("expected error for bad exponent")
	}
}

// M6：一个声称 8192+ bit 的模数明显不合理，拒绝而不是花巨大代价构造/使用这样的公钥。
func TestParseRSAJWKSetLargeModulusIsError(t *testing.T) {
	n := make([]byte, 1025) // > 8192 bit
	if _, err := rand.Read(n); err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{
		"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(n),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(65537).Bytes()),
	}
	if _, err := parseRSAJWKSet(mustJWKSJSON(t, bad)); err == nil {
		t.Fatal("expected error for modulus > 8192 bits")
	}
}

// M7：use/alg 若声明了就必须是签名用的 RS256；不匹配的键必须被跳过而不是被当成候选密钥
// 使用（即便它是结构完整的 RSA 公钥）。
func TestParseRSAJWKSetSkipsNonSigningUse(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := rsaJWKFixture("enc1", &key.PublicKey)
	enc["use"] = "enc"
	sig := rsaJWKFixture("k1", &key.PublicKey)
	keys, err := parseRSAJWKSet(mustJWKSJSON(t, enc, sig))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["enc1"]; ok {
		t.Fatal("use=enc key must be skipped")
	}
	if _, ok := keys["k1"]; !ok {
		t.Fatal("use=sig key must be kept")
	}
}

func TestParseRSAJWKSetSkipsNonRS256Alg(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other := rsaJWKFixture("other1", &key.PublicKey)
	other["alg"] = "RS384"
	sig := rsaJWKFixture("k1", &key.PublicKey)
	keys, err := parseRSAJWKSet(mustJWKSJSON(t, other, sig))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["other1"]; ok {
		t.Fatal("alg=RS384 key must be skipped")
	}
	if _, ok := keys["k1"]; !ok {
		t.Fatal("alg=RS256 key must be kept")
	}
}
