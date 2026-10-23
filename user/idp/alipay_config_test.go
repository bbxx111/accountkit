package idp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestAlipayConfigValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	ecPrivate, _ := x509.MarshalPKCS8PrivateKey(ec)
	ecPublic, _ := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic"}, NotBefore: time.Unix(0, 0), NotAfter: time.Unix(1, 0)}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(kind string, der []byte) string {
		return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
	}
	app := AlipayApp{AppID: "2026000000000001", PrivateKey: encode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)), AlipayPublicKey: encode("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&key.PublicKey))}
	for _, tc := range []struct{ name, private, public string }{
		{"pkcs1", app.PrivateKey, app.AlipayPublicKey},
		{"pkcs8_spki", encode("PRIVATE KEY", pkcs8), encode("PUBLIC KEY", spki)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := app
			candidate.PrivateKey, candidate.AlipayPublicKey = tc.private, tc.public
			if err := ValidateAlipayConfig(AlipayConfig{Apps: []AlipayApp{candidate}}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*AlipayConfig)
	}{
		{"duplicate", func(c *AlipayConfig) { c.Apps = append(c.Apps, app) }},
		{"short_app_id", func(c *AlipayConfig) { c.Apps[0].AppID = "123" }},
		{"non_ascii_app_id", func(c *AlipayConfig) { c.Apps[0].AppID = "２０２６０００００００００００１" }},
		{"missing_private", func(c *AlipayConfig) { c.Apps[0].PrivateKey = "" }},
		{"missing_public", func(c *AlipayConfig) { c.Apps[0].AlipayPublicKey = "" }},
		{"malformed_private", func(c *AlipayConfig) { c.Apps[0].PrivateKey = "synthetic-private-secret" }},
		{"malformed_public", func(c *AlipayConfig) { c.Apps[0].AlipayPublicKey = "synthetic-public-material" }},
		{"weak_private", func(c *AlipayConfig) {
			c.Apps[0].PrivateKey = encode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(weak))
		}},
		{"weak_public", func(c *AlipayConfig) {
			c.Apps[0].AlipayPublicKey = encode("RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&weak.PublicKey))
		}},
		{"ec_private", func(c *AlipayConfig) { c.Apps[0].PrivateKey = encode("PRIVATE KEY", ecPrivate) }},
		{"ec_public", func(c *AlipayConfig) { c.Apps[0].AlipayPublicKey = encode("PUBLIC KEY", ecPublic) }},
		{"certificate", func(c *AlipayConfig) { c.Apps[0].AlipayPublicKey = encode("CERTIFICATE", cert) }},
		{"extra_pem", func(c *AlipayConfig) { c.Apps[0].PrivateKey += app.PrivateKey }},
		{"auto_mode", func(c *AlipayConfig) { c.SubjectMode = "AUTO" }},
		{"lower_mode", func(c *AlipayConfig) { c.SubjectMode = "unionid" }},
		{"blank_mode", func(c *AlipayConfig) { c.SubjectMode = " " }},
		{"http_remote", func(c *AlipayConfig) { c.GatewayURL = "http://example.com/gateway.do" }},
		{"userinfo", func(c *AlipayConfig) { c.GatewayURL = "https://secret@example.com/gateway.do" }},
		{"query", func(c *AlipayConfig) { c.GatewayURL = "https://example.com/gateway.do?secret=value" }},
		{"empty_query", func(c *AlipayConfig) { c.GatewayURL = "https://example.com/gateway.do?" }},
		{"fragment", func(c *AlipayConfig) { c.GatewayURL = "https://example.com/gateway.do#secret" }},
		{"empty_fragment", func(c *AlipayConfig) { c.GatewayURL = "https://example.com/gateway.do#" }},
		{"relative", func(c *AlipayConfig) { c.GatewayURL = "/gateway.do" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := AlipayConfig{Apps: []AlipayApp{app}}
			tc.mutate(&c)
			err := ValidateAlipayConfig(c)
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			for _, secret := range []string{app.PrivateKey, app.AlipayPublicKey, "synthetic-private-secret", "synthetic-public-material", "secret=value"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("configuration material leaked in error")
				}
			}
		})
	}
	for _, gateway := range []string{"", "https://example.com/gateway.do", "http://localhost:123/gateway.do", "http://127.0.0.1:123/gateway.do", "http://[::1]:123/gateway.do"} {
		if err := ValidateAlipayConfig(AlipayConfig{Apps: []AlipayApp{app}, SubjectMode: AlipaySubjectModeOpenID, GatewayURL: gateway}); err != nil {
			t.Fatalf("valid gateway rejected: %v", err)
		}
	}
	if err := ValidateAlipayConfig(AlipayConfig{}); err != nil {
		t.Fatalf("disabled config: %v", err)
	}
	if err := ValidateAlipayConfig(AlipayConfig{SubjectMode: "AUTO"}); err == nil {
		t.Fatal("explicit invalid config ignored when disabled")
	}
}
