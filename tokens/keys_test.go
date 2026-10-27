package tokens_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/tokens"
)

func TestParseKeyList(t *testing.T) {
	got, err := tokens.ParseKeyList("1:" + testkeys.JWTBase64(1) + ", 2:" + testkeys.JWTBase64(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[1].Equal(testkeys.JWT(1)) || !got[2].Equal(testkeys.JWT(2)) {
		t.Fatalf("parsed keys mismatch")
	}
}

func TestParseKeyListRejectsWithoutEchoingInput(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384DER, _ := x509.MarshalPKCS8PrivateKey(p384)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	sec1, _ := x509.MarshalECPrivateKey(testkeys.JWT(1))
	for name, value := range map[string]string{
		"hmac random bytes": b64(bytes.Repeat([]byte{7}, 32)),
		"p384":              b64(p384DER),
		"rsa":               b64(rsaDER),
		"sec1 not pkcs8":    b64(sec1),
		"not base64":        "notbase64!",
	} {
		_, err := tokens.ParseKeyList("1:" + value)
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if strings.Contains(err.Error(), value) {
			t.Errorf("%s: error echoes input: %v", name, err)
		}
	}
	if _, err := tokens.ParsePrivateKey(nil); err == nil {
		t.Fatal("empty DER must fail")
	}
}
