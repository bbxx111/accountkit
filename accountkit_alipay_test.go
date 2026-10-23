package accountkit

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestAlipayProviderAssembly(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{AlipayApps: []AlipayApp{{AppID: "2026000000000001", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), AlipayPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)}))}}}
	providers, err := identityProviders(cfg, Deps{})
	if err != nil || len(providers) != 1 {
		t.Fatalf("configured Alipay provider not assembled: %d %v", len(providers), err)
	}
	if _, ok := providers[0].(*idp.Alipay); !ok || providers[0].Kind() != enum.IdentityAlipay {
		t.Fatal("wrong provider")
	}
	if _, err := providers[0].Verify(context.Background(), json.RawMessage(`{"app_id":"other","code":"c"}`)); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("provider configuration not used: %v", err)
	}
	cfg.AlipayApps[0].PrivateKey = "invalid"
	if _, err := identityProviders(cfg, Deps{}); err == nil {
		t.Fatal("assembly ignored invalid Alipay configuration")
	}
	if providers, err := identityProviders(Config{}, Deps{}); err != nil || len(providers) != 0 {
		t.Fatal("unconfigured Alipay should stay disabled")
	}
}
