package accountkit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"slices"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestAlipayConfigFromEnv(t *testing.T) {
	const prefix = "CUSTOM_ALIPAY_TEST_"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	public := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}))
	for _, name := range []string{"JWT_KEYS", "SUBJECT_HMAC_KEYS", "SUBJECT_CIPHER_KEYS"} {
		t.Setenv(prefix+name, "1:"+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	}
	for _, name := range []string{"JWT_ACTIVE_KEY", "SUBJECT_HMAC_ACTIVE_KEY", "SUBJECT_CIPHER_ACTIVE_KEY"} {
		t.Setenv(prefix+name, "1")
	}
	t.Setenv(prefix+"JWT_ISSUER", "issuer")
	t.Setenv(prefix+"JWT_AUDIENCE", "audience")
	t.Setenv("ACCOUNTKIT_ALIPAY_APP_IDS", "invalid-ignored")
	t.Setenv(prefix+"ALIPAY_APP_IDS", "2026000000000001, 2026000000000002")
	for _, appID := range []string{"2026000000000001", "2026000000000002"} {
		t.Setenv(prefix+"ALIPAY_"+appID+"_PRIVATE_KEY", private)
		t.Setenv(prefix+"ALIPAY_"+appID+"_PUBLIC_KEY", public)
	}
	c, err := ConfigFromEnv(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AlipayApps) != 2 || c.AlipayApps[0].AppID != "2026000000000001" || c.AlipayApps[1].AppID != "2026000000000002" || strings.TrimSpace(c.AlipayApps[0].PrivateKey) != strings.TrimSpace(private) {
		t.Fatal("multi-app PEM configuration not preserved")
	}
	if c.AlipaySubjectMode != AlipaySubjectModeUnionID || c.AlipayGatewayURL != "https://openapi.alipay.com/gateway.do" {
		t.Fatal("Alipay defaults missing")
	}
	if !slices.Contains(c.providerKinds(), enum.IdentityAlipay) {
		t.Fatal("configured Alipay absent from provider kinds")
	}
	snapshot := c.alipayConfig()
	c.AlipayApps[0].AppID = "changed"
	if snapshot.Apps[0].AppID != "2026000000000001" {
		t.Fatal("Alipay app slice aliases caller configuration")
	}
	for _, mode := range []string{"", " ", "unionid", "AUTO"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			t.Setenv(prefix+"ALIPAY_SUBJECT_MODE", mode)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"ALIPAY_SUBJECT_MODE") {
				t.Fatalf("invalid explicit mode: %v", err)
			}
		})
	}
	t.Setenv(prefix+"ALIPAY_SUBJECT_MODE", "OPENID")
	t.Setenv(prefix+"ALIPAY_GATEWAY_URL", "http://127.0.0.1:123/gateway.do")
	if c, err := ConfigFromEnv(prefix); err != nil || c.AlipaySubjectMode != AlipaySubjectModeOpenID {
		t.Fatalf("OPENID env: %v", err)
	}
	for _, list := range []string{"2026000000000001,2026000000000001", "2026000000000001,", "not-an-app-id"} {
		t.Run("apps_"+list, func(t *testing.T) {
			t.Setenv(prefix+"ALIPAY_APP_IDS", list)
			if _, err := ConfigFromEnv(prefix); err == nil {
				t.Fatal("invalid app list accepted")
			}
		})
	}
	t.Setenv(prefix+"OIDC_PROVIDERS", "ALIPAY")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "OIDC_PROVIDERS") {
		t.Fatalf("OIDC may not occupy ALIPAY: %v", err)
	}
	if field, err := validateOIDCProvider(OIDCProvider{Kind: enum.IdentityAlipay}); err == nil || field != "Kind" {
		t.Fatal("Go OIDC configuration may not occupy ALIPAY")
	}
}
