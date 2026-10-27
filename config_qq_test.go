package accountkit

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
)

func TestQQConfigFromEnv(t *testing.T) {
	const prefix = "CUSTOM_QQ_TEST_"
	for _, name := range []string{"JWT_KEYS", "SUBJECT_HMAC_KEYS", "SUBJECT_CIPHER_KEYS"} {
		t.Setenv(prefix+name, "1:"+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	}
	t.Setenv(prefix+"JWT_KEYS", "1:"+testkeys.JWTBase64(1))
	for _, name := range []string{"JWT_ACTIVE_KEY", "SUBJECT_HMAC_ACTIVE_KEY", "SUBJECT_CIPHER_ACTIVE_KEY"} {
		t.Setenv(prefix+name, "1")
	}
	t.Setenv(prefix+"JWT_ISSUER", "issuer")
	t.Setenv(prefix+"JWT_AUDIENCE", "audience")
	t.Setenv("ACCOUNTKIT_QQ_APP_IDS", "invalid-ignored")
	c, err := ConfigFromEnv(prefix)
	if err != nil || len(c.QQAppIDs) != 0 || slices.Contains(c.providerKinds(), enum.IdentityQQ) {
		t.Fatalf("unset QQ must stay disabled: %v", err)
	}
	t.Setenv(prefix+"QQ_APP_IDS", "1100000001, 1100000002")
	c, err = ConfigFromEnv(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.QQAppIDs, []string{"1100000001", "1100000002"}) || c.QQSubjectMode != QQSubjectModeUnionID || c.QQAPIBaseURL != "https://graph.qq.com" {
		t.Fatalf("QQ env defaults: %+v", c.QQAppIDs)
	}
	if !slices.Contains(c.providerKinds(), enum.IdentityQQ) {
		t.Fatal("configured QQ absent from provider kinds")
	}
	snapshot := c.qqConfig()
	c.QQAppIDs[0] = "changed"
	if snapshot.AppIDs[0] != "1100000001" {
		t.Fatal("QQ app slice aliases caller configuration")
	}
	for _, mode := range []string{"", " ", "unionid", "AUTO"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			t.Setenv(prefix+"QQ_SUBJECT_MODE", mode)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"QQ_SUBJECT_MODE") {
				t.Fatalf("invalid explicit mode: %v", err)
			}
		})
	}
	t.Setenv(prefix+"QQ_SUBJECT_MODE", "OPENID")
	t.Setenv(prefix+"QQ_API_BASE_URL", "http://127.0.0.1:123")
	if c, err := ConfigFromEnv(prefix); err != nil || c.QQSubjectMode != QQSubjectModeOpenID || c.QQAPIBaseURL != "http://127.0.0.1:123" {
		t.Fatalf("OPENID env: %v", err)
	}
	t.Setenv(prefix+"QQ_API_BASE_URL", "http://graph.qq.com")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "QQAPIBaseURL") {
		t.Fatalf("insecure base URL accepted: %v", err)
	}
	t.Setenv(prefix+"QQ_API_BASE_URL", "")
	for _, list := range []string{"1100000001,1100000001", "1100000001,", "app-one"} {
		t.Run("apps_"+list, func(t *testing.T) {
			t.Setenv(prefix+"QQ_APP_IDS", list)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"QQ_APP_IDS") || strings.Contains(err.Error(), "app-one") {
				t.Fatalf("invalid app list accepted or echoed: %v", err)
			}
		})
	}
	t.Setenv(prefix+"QQ_APP_IDS", "1100000001")
	t.Setenv(prefix+"OIDC_PROVIDERS", "QQ")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "OIDC_PROVIDERS") {
		t.Fatalf("OIDC may not occupy QQ: %v", err)
	}
	if field, err := validateOIDCProvider(OIDCProvider{Kind: enum.IdentityQQ}); err == nil || field != "Kind" {
		t.Fatal("Go OIDC configuration may not occupy QQ")
	}
	if err := (Config{QQAppIDs: []string{"x"}}).validateBase(); err == nil || !strings.HasPrefix(err.Error(), "accountkit: idp: QQAppIDs[0]") {
		t.Fatalf("Validate must report QQ field: %v", err)
	}
}
