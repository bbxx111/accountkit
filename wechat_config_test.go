package accountkit_test

import (
	"strings"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
)

func TestWeChatMiniProgramConfig(t *testing.T) {
	c := minimal()
	c.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityWeChat}
	c.RequiredIdentityKinds = []enum.IdentityKind{}
	c.WeChatMiniProgramApps = []accountkit.WeChatApp{{AppID: "mini", Secret: "secret:with:colons"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []accountkit.WeChatSubjectMode{"AUTO", " ", "openid"} {
		c.WeChatSubjectMode = mode
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "WeChatSubjectMode") {
			t.Fatalf("invalid mode: %v", err)
		}
	}
	c.WeChatSubjectMode = accountkit.WeChatSubjectModeOpenID
	for _, app := range []accountkit.WeChatApp{{AppID: "mini", Secret: "secret-marker"}, {AppID: "bad:app", Secret: "secret-marker"}} {
		c.WeChatApps = []accountkit.WeChatApp{app}
		err := c.Validate()
		if err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("unsafe or missing error: %v", err)
		}
	}
}

func TestWeChatMiniProgramEnv(t *testing.T) {
	const p = "MINI_CONFIG_"
	t.Setenv(p+"JWT_KEYS", "1:"+testkeys.JWTBase64(1))
	t.Setenv(p+"JWT_ACTIVE_KEY", "1")
	t.Setenv(p+"JWT_ISSUER", "issuer")
	t.Setenv(p+"JWT_AUDIENCE", "consumer")
	t.Setenv(p+"SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv(p+"SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv(p+"SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv(p+"SUBJECT_CIPHER_ACTIVE_KEY", "1")
	t.Setenv(p+"WECHAT_MINI_PROGRAM_APPS", "mini:secret:colon")
	c, err := accountkit.ConfigFromEnv(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.WeChatSubjectMode != accountkit.WeChatSubjectModeUnionID || len(c.WeChatMiniProgramApps) != 1 || c.WeChatMiniProgramApps[0].Secret != "secret:colon" {
		t.Fatal("mini program defaults not loaded")
	}
	for _, mode := range []string{"", " ", "AUTO", "openid"} {
		t.Setenv(p+"WECHAT_SUBJECT_MODE", mode)
		_, err = accountkit.ConfigFromEnv(p)
		if err == nil || !strings.Contains(err.Error(), p+"WECHAT_SUBJECT_MODE") {
			t.Fatalf("mode must fail: %v", err)
		}
	}
}

func TestWeChatMiniProgramInvalidApps(t *testing.T) {
	for _, apps := range [][]accountkit.WeChatApp{
		{{AppID: "mini", Secret: "secret-marker"}, {AppID: "mini", Secret: "secret-marker"}},
		{{AppID: "bad:mini", Secret: "secret-marker"}},
		{{AppID: " ", Secret: "secret-marker"}},
		{{AppID: "mini", Secret: " "}},
	} {
		c := minimal()
		c.WeChatMiniProgramApps = apps
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "WeChatMiniProgramApps") || strings.Contains(err.Error(), "secret-marker") {
			t.Fatalf("unsafe or missing validation: %v", err)
		}
	}
}
