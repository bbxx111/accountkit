package accountkit_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
)

func TestFacebookRequiredKindRejected(t *testing.T) {
	c := minimal()
	c.FacebookAppID, c.FacebookAppSecret = "123", "s3cr3t-value"
	c.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityFacebook}
	err := c.Validate()
	if err == nil {
		_, err = accountkit.New(c, baseDeps(t))
	}
	if err == nil || !strings.Contains(err.Error(), "RequiredIdentityKinds") || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("FACEBOOK in RequiredIdentityKinds must fail naming RequiredIdentityKinds: %v", err)
	}
}

func TestFacebookFieldsIgnoredWhenAppIDEmpty(t *testing.T) {
	c := minimal()
	c.FacebookGraphAPIVersion = "bad"
	c.FacebookNonceTTL = time.Second
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled facebook must not validate its other fields: %v", err)
	}
}

func TestFacebookConfigValidation(t *testing.T) {
	c := minimal()
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled facebook: %v", err)
	}
	c.FacebookAppID = "123"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookAppSecret") {
		t.Fatalf("missing secret: %v", err)
	}
	c.FacebookAppSecret = "s3cr3t-value"
	c.FacebookGraphAPIVersion = "26"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookGraphAPIVersion") || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("version: %v", err)
	}
	c.FacebookGraphAPIVersion = ""
	c.FacebookNonceTTL = time.Second
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookNonceTTL") {
		t.Fatalf("ttl: %v", err)
	}
	c.FacebookNonceTTL = 0
	c.FacebookJWKSURL = "ftp://x"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookJWKSURL") {
		t.Fatalf("jwks: %v", err)
	}
	c.FacebookJWKSURL = ""
	c.FacebookGraphAPIBaseURL = "graph.example"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FacebookGraphAPIBaseURL") {
		t.Fatalf("graph url: %v", err)
	}
}

func TestFacebookConfigFromEnv(t *testing.T) {
	for name, value := range map[string]string{"JWT_KEYS": "1:" + testkeys.JWTBase64(1), "JWT_ACTIVE_KEY": "1", "JWT_ISSUER": "issuer", "JWT_AUDIENCE": "aud", "SUBJECT_HMAC_KEYS": "1:" + b64(k(2)), "SUBJECT_HMAC_ACTIVE_KEY": "1", "SUBJECT_CIPHER_KEYS": "1:" + b64(k(3)), "SUBJECT_CIPHER_ACTIVE_KEY": "1"} {
		t.Setenv("FBENV_"+name, value)
	}
	t.Setenv("FBENV_FACEBOOK_APP_ID", "123")
	if _, err := accountkit.ConfigFromEnv("FBENV_"); err == nil || !strings.Contains(err.Error(), "FacebookAppSecret") {
		t.Fatalf("env missing secret: %v", err)
	}
	t.Setenv("FBENV_FACEBOOK_APP_SECRET", "s")
	t.Setenv("FBENV_FACEBOOK_NONCE_TTL", "5m")
	t.Setenv("FBENV_FACEBOOK_GRAPH_API_BASE_URL", "http://127.0.0.1:3")
	t.Setenv("FBENV_FACEBOOK_JWKS_URL", "http://127.0.0.1:4/k")
	c, err := accountkit.ConfigFromEnv("FBENV_")
	if err != nil || c.FacebookAppID != "123" || c.FacebookGraphAPIVersion != "v26.0" || c.FacebookNonceTTL != 5*time.Minute ||
		c.FacebookGraphAPIBaseURL != "http://127.0.0.1:3" || c.FacebookJWKSURL != "http://127.0.0.1:4/k" {
		t.Fatalf("env: %+v %v", c, err)
	}
	t.Setenv("FBENV_FACEBOOK_GRAPH_API_VERSION", "bad")
	if _, err := accountkit.ConfigFromEnv("FBENV_"); err == nil || !strings.Contains(err.Error(), "FacebookGraphAPIVersion") {
		t.Fatalf("env version: %v", err)
	}
}

func TestFacebookDefaultEnablement(t *testing.T) {
	c := minimal()
	c.FacebookAppID, c.FacebookAppSecret = "123", "s"
	a, err := accountkit.New(c, baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.Users().IdentityPolicy().EnabledKinds(), enum.IdentityFacebook) {
		t.Fatal("FACEBOOK must be enabled by default when App ID is set")
	}
	off, err := accountkit.New(minimal(), baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(off.Users().IdentityPolicy().EnabledKinds(), enum.IdentityFacebook) {
		t.Fatal("FACEBOOK must be disabled without App ID")
	}
	c = minimal()
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail, enum.IdentityFacebook}
	c.RequiredIdentityKinds = []enum.IdentityKind{}
	if _, err := accountkit.New(c, baseDeps(t)); err == nil || !strings.Contains(err.Error(), "FACEBOOK") {
		t.Fatalf("explicit FACEBOOK without App ID must fail naming FACEBOOK: %v", err)
	}
}
