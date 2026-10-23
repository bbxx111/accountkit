package accountkit

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestWeiboConfigFromEnv(t *testing.T) {
	const prefix = "CUSTOM_WEIBO_TEST_"
	for _, name := range []string{"JWT_KEYS", "SUBJECT_HMAC_KEYS", "SUBJECT_CIPHER_KEYS"} {
		t.Setenv(prefix+name, "1:"+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	}
	for _, name := range []string{"JWT_ACTIVE_KEY", "SUBJECT_HMAC_ACTIVE_KEY", "SUBJECT_CIPHER_ACTIVE_KEY"} {
		t.Setenv(prefix+name, "1")
	}
	t.Setenv(prefix+"JWT_ISSUER", "issuer")
	t.Setenv(prefix+"JWT_AUDIENCE", "audience")
	t.Setenv("ACCOUNTKIT_WEIBO_APP_IDS", "invalid-ignored")
	c, err := ConfigFromEnv(prefix)
	if err != nil || len(c.WeiboAppIDs) != 0 || slices.Contains(c.providerKinds(), enum.IdentityWeibo) {
		t.Fatalf("unset Weibo must stay disabled: %v", err)
	}
	t.Setenv(prefix+"WEIBO_APP_IDS", "1234567890, 1234567891")
	c, err = ConfigFromEnv(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.WeiboAppIDs, []string{"1234567890", "1234567891"}) || c.WeiboAPIBaseURL != "https://api.weibo.com" {
		t.Fatalf("Weibo env defaults: %+v %q", c.WeiboAppIDs, c.WeiboAPIBaseURL)
	}
	if !slices.Contains(c.providerKinds(), enum.IdentityWeibo) {
		t.Fatal("configured Weibo absent from provider kinds")
	}
	snapshot := c.weiboConfig()
	c.WeiboAppIDs[0] = "changed"
	if snapshot.AppIDs[0] != "1234567890" {
		t.Fatal("Weibo app slice aliases caller configuration")
	}
	t.Setenv(prefix+"WEIBO_API_BASE_URL", "http://127.0.0.1:123")
	if c, err := ConfigFromEnv(prefix); err != nil || c.WeiboAPIBaseURL != "http://127.0.0.1:123" {
		t.Fatalf("loopback base URL: %v", err)
	}
	t.Setenv(prefix+"WEIBO_API_BASE_URL", "http://api.weibo.com")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "WeiboAPIBaseURL") {
		t.Fatalf("insecure base URL accepted: %v", err)
	}
	t.Setenv(prefix+"WEIBO_API_BASE_URL", "")
	for _, list := range []string{"1234567890,1234567890", "1234567890,", "app-key"} {
		t.Run("apps_"+list, func(t *testing.T) {
			t.Setenv(prefix+"WEIBO_APP_IDS", list)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"WEIBO_APP_IDS") || strings.Contains(err.Error(), "app-key") {
				t.Fatalf("invalid app list accepted or echoed: %v", err)
			}
		})
	}
	t.Setenv(prefix+"WEIBO_APP_IDS", "1234567890")
	t.Setenv(prefix+"OIDC_PROVIDERS", "WEIBO")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "OIDC_PROVIDERS") {
		t.Fatalf("OIDC may not occupy WEIBO: %v", err)
	}
	if field, err := validateOIDCProvider(OIDCProvider{Kind: enum.IdentityWeibo}); err == nil || field != "Kind" {
		t.Fatal("Go OIDC configuration may not occupy WEIBO")
	}
	if err := (Config{WeiboAppIDs: []string{"x"}}).validateBase(); err == nil || !strings.HasPrefix(err.Error(), "accountkit: idp: WeiboAppIDs[0]") {
		t.Fatalf("Validate must report Weibo field: %v", err)
	}
}
