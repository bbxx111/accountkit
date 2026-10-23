package accountkit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
)

func corpOIDC() accountkit.OIDCProvider {
	return accountkit.OIDCProvider{Kind: "CORP_SSO", Issuer: "https://sso.corp.example", JWKSURL: "https://sso.corp.example/jwks", ClientIDs: []string{"corp-web"}}
}

func TestOIDCProviderConfigValidation(t *testing.T) {
	ok := minimal()
	ok.OIDCProviders = []accountkit.OIDCProvider{corpOIDC()}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.OIDCProviders[0].NonceTTL != 0 {
		t.Fatal("Validate must not write defaults into the caller's slice")
	}
	for name, tc := range map[string]struct {
		mutate func(*accountkit.OIDCProvider)
		want   string
	}{
		"kind format":  {func(p *accountkit.OIDCProvider) { p.Kind = "corp" }, "OIDCProviders[0].Kind"},
		"builtin kind": {func(p *accountkit.OIDCProvider) { p.Kind = enum.IdentityGoogle }, "OIDCProviders[0].Kind"},
		"anchor kind":  {func(p *accountkit.OIDCProvider) { p.Kind = enum.IdentityPhone }, "OIDCProviders[0].Kind"},
		"issuer":       {func(p *accountkit.OIDCProvider) { p.Issuer = "sso.corp.example" }, "OIDCProviders[0].Issuer"},
		"jwks":         {func(p *accountkit.OIDCProvider) { p.JWKSURL = "ftp://x/keys" }, "OIDCProviders[0].JWKSURL"},
		"no clients":   {func(p *accountkit.OIDCProvider) { p.ClientIDs = nil }, "OIDCProviders[0].ClientIDs"},
		"dup clients":  {func(p *accountkit.OIDCProvider) { p.ClientIDs = []string{"a", "a"} }, "OIDCProviders[0].ClientIDs"},
		"nonce ttl":    {func(p *accountkit.OIDCProvider) { p.NonceTTL = 30 * time.Second }, "OIDCProviders[0].NonceTTL"},
		"empty claim":  {func(p *accountkit.OIDCProvider) { p.NameClaims = []string{""} }, "OIDCProviders[0].NameClaims"},
		"dup picture":  {func(p *accountkit.OIDCProvider) { p.PictureClaims = []string{"p", "p"} }, "OIDCProviders[0].PictureClaims"},
	} {
		c := minimal()
		p := corpOIDC()
		tc.mutate(&p)
		c.OIDCProviders = []accountkit.OIDCProvider{p}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	dup := minimal()
	dup.OIDCProviders = []accountkit.OIDCProvider{corpOIDC(), corpOIDC()}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate kind: %v", err)
	}
	required := minimal()
	required.OIDCProviders = []accountkit.OIDCProvider{corpOIDC()}
	required.EnabledIdentityKinds = []enum.IdentityKind{"CORP_SSO", enum.IdentityEmail}
	required.RequiredIdentityKinds = []enum.IdentityKind{"CORP_SSO"}
	if err := required.Validate(); err == nil {
		t.Fatal("OIDC kind accepted as required")
	}
	unregistered := minimal()
	unregistered.EnabledIdentityKinds = []enum.IdentityKind{"CORP_SSO", enum.IdentityEmail}
	unregistered.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	if err := unregistered.Validate(); err == nil || !strings.Contains(err.Error(), "EnabledIdentityKinds") {
		t.Fatalf("enabled unregistered OIDC kind: %v", err)
	}
	unregistered.OIDCProviders = []accountkit.OIDCProvider{corpOIDC()}
	if err := unregistered.Validate(); err != nil {
		t.Fatalf("enabled configured OIDC kind: %v", err)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv("T_JWT_ACTIVE_KEY", "1")
	t.Setenv("T_JWT_ISSUER", "shifang")
	t.Setenv("T_JWT_AUDIENCE", "app")
	t.Setenv("T_SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv("T_SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv("T_SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv("T_SUBJECT_CIPHER_ACTIVE_KEY", "1")
}

func TestConfigFromEnvOIDC(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("T_OIDC_PROVIDERS", "CORP_SSO, PARTNER_SSO")
	t.Setenv("T_OIDC_CORP_SSO_ISSUER", "https://sso.corp.example")
	t.Setenv("T_OIDC_CORP_SSO_JWKS_URL", "https://sso.corp.example/jwks")
	t.Setenv("T_OIDC_CORP_SSO_CLIENT_IDS", "corp-web, corp-ios")
	t.Setenv("T_OIDC_CORP_SSO_NONCE_TTL", "15m")
	t.Setenv("T_OIDC_CORP_SSO_NAME_CLAIMS", "name, preferred_username")
	t.Setenv("T_OIDC_PARTNER_SSO_ISSUER", "https://partner.example")
	t.Setenv("T_OIDC_PARTNER_SSO_JWKS_URL", "https://partner.example/keys")
	t.Setenv("T_OIDC_PARTNER_SSO_CLIENT_IDS", "partner")
	t.Setenv("T_OIDC_PARTNER_SSO_NAME_CLAIMS", "")
	t.Setenv("T_OIDC_PARTNER_SSO_PICTURE_CLAIMS", "")
	c, err := accountkit.ConfigFromEnv("T_")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.OIDCProviders) != 2 {
		t.Fatalf("%+v", c.OIDCProviders)
	}
	corp, partner := c.OIDCProviders[0], c.OIDCProviders[1]
	if corp.Kind != "CORP_SSO" || len(corp.ClientIDs) != 2 || corp.ClientIDs[1] != "corp-ios" || corp.NonceTTL != 15*time.Minute ||
		len(corp.NameClaims) != 2 || corp.NameClaims[1] != "preferred_username" || corp.PictureClaims != nil {
		t.Fatalf("corp: %+v", corp)
	}
	if partner.NonceTTL != 10*time.Minute || partner.NameClaims == nil || len(partner.NameClaims) != 0 || partner.PictureClaims == nil || len(partner.PictureClaims) != 0 {
		t.Fatalf("partner: unset vs empty claim lists must differ: %+v", partner)
	}
}

func TestConfigFromEnvOIDCErrorsNameVariables(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"T_OIDC_PROVIDERS": "corp"}, "T_OIDC_PROVIDERS"},
		{map[string]string{"T_OIDC_PROVIDERS": "GOOGLE"}, "T_OIDC_PROVIDERS"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO,CORP_SSO"}, "T_OIDC_PROVIDERS"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO", "T_OIDC_CORP_SSO_JWKS_URL": "https://x/jwks", "T_OIDC_CORP_SSO_CLIENT_IDS": "a"}, "T_OIDC_CORP_SSO_ISSUER"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO", "T_OIDC_CORP_SSO_ISSUER": "https://x", "T_OIDC_CORP_SSO_CLIENT_IDS": "a"}, "T_OIDC_CORP_SSO_JWKS_URL"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO", "T_OIDC_CORP_SSO_ISSUER": "https://x", "T_OIDC_CORP_SSO_JWKS_URL": "https://x/jwks"}, "T_OIDC_CORP_SSO_CLIENT_IDS"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO", "T_OIDC_CORP_SSO_ISSUER": "https://x", "T_OIDC_CORP_SSO_JWKS_URL": "https://x/jwks", "T_OIDC_CORP_SSO_CLIENT_IDS": "a", "T_OIDC_CORP_SSO_NONCE_TTL": "10s"}, "T_OIDC_CORP_SSO_NONCE_TTL"},
		{map[string]string{"T_OIDC_PROVIDERS": "CORP_SSO", "T_OIDC_CORP_SSO_ISSUER": "secret-host", "T_OIDC_CORP_SSO_JWKS_URL": "https://x/jwks", "T_OIDC_CORP_SSO_CLIENT_IDS": "a"}, "T_OIDC_CORP_SSO_ISSUER"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			setRequiredEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := accountkit.ConfigFromEnv("T_")
			if err == nil || !strings.Contains(err.Error(), tc.want+":") {
				t.Fatalf("got %v, want variable %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-host") {
				t.Fatalf("value leaked: %v", err)
			}
		})
	}
}
