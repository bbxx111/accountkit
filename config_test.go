package accountkit_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
)

func k(b byte) []byte     { return bytes.Repeat([]byte{b}, 32) }
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// minimal 返回只含必填项的配置。
func minimal() accountkit.Config {
	return accountkit.Config{
		JWTKeys: map[uint16][]byte{1: k(1)}, JWTActiveKey: 1, JWTIssuer: "shifang", JWTAudience: "app",
		SubjectHMACKeys: map[uint16][]byte{1: k(2)}, SubjectHMACActiveKey: 1,
		SubjectCipherKeys: map[uint16][]byte{1: k(3)}, SubjectCipherActiveKey: 1,
	}
}

func TestAuditConfiguration(t *testing.T) {
	c := minimal()
	for _, tc := range []struct {
		source, namespace string
		valid             bool
	}{
		{"accountkit/test", "urn:身份:消费者", true}, {strings.Repeat("a", 128), strings.Repeat("n", 512), true},
		{"bad source", "namespace", false}, {strings.Repeat("a", 129), "namespace", false},
		{"source", strings.Repeat("n", 513), false}, {"source", "bad\x00namespace", false}, {"source", string([]byte{0xff}), false},
	} {
		c.AuditSource = tc.source
		c.AuditNamespace = tc.namespace
		if err := c.Validate(); (err == nil) != tc.valid {
			t.Fatalf("audit configuration validity=%v, error=%v", tc.valid, err)
		}
	}
	const prefix = "AUDIT_TEST_"
	t.Setenv(prefix+"JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv(prefix+"JWT_ACTIVE_KEY", "1")
	t.Setenv(prefix+"JWT_ISSUER", "issuer")
	t.Setenv(prefix+"JWT_AUDIENCE", "app")
	t.Setenv(prefix+"SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv(prefix+"SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv(prefix+"SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv(prefix+"SUBJECT_CIPHER_ACTIVE_KEY", "1")
	t.Setenv(prefix+"AUTH_SCHEMA", "custom")
	t.Setenv(prefix+"AUDIT_SOURCE", "")
	t.Setenv(prefix+"AUDIT_NAMESPACE", "")
	for _, tc := range []struct{ name, source, namespace string }{
		{"missing_both", "", ""},
		{"source_only", "test/auth", ""},
		{"namespace_only", "", "urn:test:users"},
		{"explicit_local_defaults", "accountkit/custom", "accountkit/custom/resources"},
		{"explicit_custom", "test/auth", "urn:test:users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(prefix+"AUDIT_SOURCE", tc.source)
			t.Setenv(prefix+"AUDIT_NAMESPACE", tc.namespace)
			if tc.source == "" {
				if err := os.Unsetenv(prefix + "AUDIT_SOURCE"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.namespace == "" {
				if err := os.Unsetenv(prefix + "AUDIT_NAMESPACE"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := accountkit.ConfigFromEnv(prefix)
			if err != nil {
				t.Fatal(err)
			}
			deps := baseDeps(t)
			deps.Audit = audit.Noop{}
			auth, newErr := accountkit.New(got, deps)
			explicit := tc.source != "" && tc.namespace != ""
			if (newErr == nil) != explicit {
				t.Fatalf("external config explicit=%v: %v", explicit, newErr)
			}
			if auth != nil {
				auth.Close()
			}
			if got.AuditSource != tc.source || got.AuditNamespace != tc.namespace {
				t.Fatalf("explicit audit input lost: source=%q namespace=%q", got.AuditSource, got.AuditNamespace)
			}
			if got.Schema != "custom" || got.KeyPrefix != "auth:" || got.AccessTokenTTL != 15*time.Minute {
				t.Fatal("existing configuration defaults changed")
			}
			if err := got.Validate(); err != nil {
				t.Fatal(err)
			}
			if got.AuditSource != tc.source || got.AuditNamespace != tc.namespace {
				t.Fatal("Validate filled audit defaults back into caller configuration")
			}
		})
	}
}

func TestValidateAppliesDefaults(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "T_") {
			t.Setenv(name, "")
		}
	}
	c := minimal()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// Validate 不改动 c（值接收者）；用 ConfigFromEnv 路径观察默认值更直接：
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv("T_JWT_ACTIVE_KEY", "1")
	t.Setenv("T_JWT_ISSUER", "shifang")
	t.Setenv("T_JWT_AUDIENCE", "app")
	t.Setenv("T_SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv("T_SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv("T_SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv("T_SUBJECT_CIPHER_ACTIVE_KEY", "1")
	got, err := accountkit.ConfigFromEnv("T_")
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "account" || got.KeyPrefix != "auth:" || got.AccessTokenTTL != 15*time.Minute ||
		got.RefreshTokenTTL != 720*time.Hour || got.RefreshGrace != 30*time.Second || got.ReauthMaxAge != 5*time.Minute ||
		got.SensitiveOpVerification == nil || !*got.SensitiveOpVerification ||
		got.ProviderProfileBackfill == nil || !*got.ProviderProfileBackfill ||
		got.CodeTTL != 5*time.Minute || got.CodeMaxAttempts != 5 || got.CodeCooldown != 60*time.Second ||
		got.CodeDailyLimitPerTarget != 10 || got.CodeDailyLimitPerIP != 100 || got.MaxIdentitiesPerKind != 1 ||
		got.CodeFailureLimitPerTarget != 10 || got.CodeFailureWindow != 15*time.Minute ||
		got.DeletionCoolingPeriod != 360*time.Hour || got.AuditRetentionDays != 180 || got.DefaultRegion != "CN" ||
		got.MaintenanceInterval != 5*time.Minute {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

func TestConfigFromEnvOverridesAndParses(t *testing.T) {
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1))+",2:"+b64(k(9)))
	t.Setenv("T_JWT_ACTIVE_KEY", "2")
	t.Setenv("T_JWT_ISSUER", "shifang")
	t.Setenv("T_JWT_AUDIENCE", "app")
	t.Setenv("T_SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv("T_SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv("T_SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv("T_SUBJECT_CIPHER_ACTIVE_KEY", "1")
	t.Setenv("T_AUTH_SCHEMA", "auth_staging")
	t.Setenv("T_AUTH_KEY_PREFIX", "stg:")
	t.Setenv("T_ACCESS_TOKEN_TTL", "10m")
	t.Setenv("T_SENSITIVE_OP_VERIFICATION", "false")
	t.Setenv("T_PROVIDER_PROFILE_BACKFILL", "false")
	t.Setenv("T_CODE_MAX_ATTEMPTS", "3")
	t.Setenv("T_CODE_FAILURE_LIMIT_PER_TARGET", "12")
	t.Setenv("T_CODE_FAILURE_WINDOW", "20m")
	t.Setenv("T_DEFAULT_REGION", "us")
	got, err := accountkit.ConfigFromEnv("T_")
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "auth_staging" || got.KeyPrefix != "stg:" || got.AccessTokenTTL != 10*time.Minute ||
		got.JWTActiveKey != 2 || len(got.JWTKeys) != 2 || *got.SensitiveOpVerification || got.ProviderProfileBackfill == nil || *got.ProviderProfileBackfill || got.CodeMaxAttempts != 3 || got.DefaultRegion != "US" || got.CodeFailureLimitPerTarget != 12 || got.CodeFailureWindow != 20*time.Minute {
		t.Fatalf("overrides not applied: %+v", got)
	}
}

func TestConfigFromEnvSchema(t *testing.T) {
	const prefix = "SCHEMA_TEST_"
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, prefix) {
			t.Setenv(name, "")
		}
	}
	for name, value := range map[string]string{
		"JWT_KEYS": "1:" + b64(k(1)), "JWT_ACTIVE_KEY": "1",
		"JWT_ISSUER": "test-issuer", "JWT_AUDIENCE": "consumer",
		"SUBJECT_HMAC_KEYS": "1:" + b64(k(2)), "SUBJECT_HMAC_ACTIVE_KEY": "1",
		"SUBJECT_CIPHER_KEYS": "1:" + b64(k(3)), "SUBJECT_CIPHER_ACTIVE_KEY": "1",
	} {
		t.Setenv(prefix+name, value)
	}
	for _, tc := range []struct{ name, value, want string }{
		{"unset", "", "account"},
		{"empty", "", "account"},
		{"account", "account", "account"},
		{"auth", "auth", "auth"},
		{"custom", "custom_account", "custom_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(prefix+"AUTH_SCHEMA", tc.value)
			if tc.name == "unset" {
				if err := os.Unsetenv(prefix + "AUTH_SCHEMA"); err != nil {
					t.Fatal(err)
				}
			}
			c, err := accountkit.ConfigFromEnv(prefix)
			if err != nil {
				t.Fatal(err)
			}
			if c.Schema != tc.want || c.KeyPrefix != "auth:" {
				t.Fatalf("schema=%q prefix=%q, want schema=%q prefix=auth:", c.Schema, c.KeyPrefix, tc.want)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*accountkit.Config){
		"missing jwt keys":          func(c *accountkit.Config) { c.JWTKeys = nil },
		"jwt active missing":        func(c *accountkit.Config) { c.JWTActiveKey = 9 },
		"jwt key short":             func(c *accountkit.Config) { c.JWTKeys = map[uint16][]byte{1: k(1)[:16]} },
		"issuer empty":              func(c *accountkit.Config) { c.JWTIssuer = "" },
		"audience empty":            func(c *accountkit.Config) { c.JWTAudience = "" },
		"hmac active missing":       func(c *accountkit.Config) { c.SubjectHMACActiveKey = 7 },
		"cipher keys missing":       func(c *accountkit.Config) { c.SubjectCipherKeys = nil },
		"cipher key not 32 bytes":   func(c *accountkit.Config) { c.SubjectCipherKeys = map[uint16][]byte{1: bytes.Repeat([]byte{3}, 64)} },
		"bad schema":                func(c *accountkit.Config) { c.Schema = "Auth-1" },
		"prefix without colon":      func(c *accountkit.Config) { c.KeyPrefix = "auth" },
		"access ttl too short":      func(c *accountkit.Config) { c.AccessTokenTTL = 10 * time.Second },
		"refresh shorter than acc":  func(c *accountkit.Config) { c.AccessTokenTTL = time.Hour; c.RefreshTokenTTL = 30 * time.Minute },
		"grace >= access":           func(c *accountkit.Config) { c.RefreshGrace = 20 * time.Minute },
		"attempts zero":             func(c *accountkit.Config) { c.CodeMaxAttempts = -1 },
		"failure limit negative":    func(c *accountkit.Config) { c.CodeFailureLimitPerTarget = -1 },
		"failure window sub-second": func(c *accountkit.Config) { c.CodeFailureWindow = 500 * time.Millisecond },
		"code ttl sub-second":       func(c *accountkit.Config) { c.CodeTTL = 500 * time.Millisecond },
		"code cooldown sub-second":  func(c *accountkit.Config) { c.CodeCooldown = 500 * time.Millisecond },
		"max identities zero":       func(c *accountkit.Config) { c.MaxIdentitiesPerKind = -1 },
		"region not 2 letters":      func(c *accountkit.Config) { c.DefaultRegion = "CHN" },
		"maintenance too short":     func(c *accountkit.Config) { c.MaintenanceInterval = 500 * time.Millisecond },
	}
	for name, mutate := range cases {
		c := minimal()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestConfigFromEnvReportsBadValues(t *testing.T) {
	t.Setenv("T_JWT_KEYS", "1:notbase64!")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil || !strings.Contains(err.Error(), "T_JWT_KEYS") {
		t.Fatalf("bad key list must name the variable: %v", err)
	}
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv("T_JWT_ACTIVE_KEY", "one")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil || !strings.Contains(err.Error(), "T_JWT_ACTIVE_KEY") {
		t.Fatalf("bad integer must name the variable: %v", err)
	}
	t.Setenv("T_JWT_ACTIVE_KEY", "1")
	t.Setenv("T_ACCESS_TOKEN_TTL", "fifteen")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil || !strings.Contains(err.Error(), "T_ACCESS_TOKEN_TTL") {
		t.Fatalf("bad duration must name the variable: %v", err)
	}
	t.Setenv("T_ACCESS_TOKEN_TTL", "")
	t.Setenv("T_PROVIDER_PROFILE_BACKFILL", "maybe")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil || !strings.Contains(err.Error(), "T_PROVIDER_PROFILE_BACKFILL") {
		t.Fatalf("bad boolean must name the variable: %v", err)
	}
}

func TestIdPConfigDefaultsAndValidation(t *testing.T) {
	c := minimal()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// Validate 不改动 c（值接收者，见 TestValidateAppliesDefaults）；用 ConfigFromEnv 路径观察默认值。
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv("T_JWT_ACTIVE_KEY", "1")
	t.Setenv("T_JWT_ISSUER", "shifang")
	t.Setenv("T_JWT_AUDIENCE", "app")
	t.Setenv("T_SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv("T_SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv("T_SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv("T_SUBJECT_CIPHER_ACTIVE_KEY", "1")
	def, err := accountkit.ConfigFromEnv("T_")
	if err != nil {
		t.Fatal(err)
	}
	if def.AppleNonceTTL != 10*time.Minute || def.WeChatAPIBaseURL != "https://api.weixin.qq.com" || def.AppleJWKSURL != "https://appleid.apple.com/auth/keys" {
		t.Fatalf("defaults: %+v", def)
	}
	if len(def.WeChatApps) != 0 || len(def.AppleBundleIDs) != 0 {
		t.Fatal("IdPs disabled by default")
	}

	bad := func(mutate func(*accountkit.Config), wantSub string) {
		t.Helper()
		c := minimal()
		mutate(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), wantSub) {
			t.Fatalf("want error containing %q, got %v", wantSub, err)
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Fatalf("secret leaked into error: %v", err)
		}
	}
	bad(func(c *accountkit.Config) { c.WeChatApps = []accountkit.WeChatApp{{AppID: "", Secret: "s3cret"}} }, "WeChatApps")
	bad(func(c *accountkit.Config) { c.WeChatApps = []accountkit.WeChatApp{{AppID: "wx1", Secret: ""}} }, "WeChatApps")
	bad(func(c *accountkit.Config) {
		c.WeChatApps = []accountkit.WeChatApp{{AppID: "wx1", Secret: "s3cret"}, {AppID: "wx1", Secret: "s3cret"}}
	}, "duplicate")
	bad(func(c *accountkit.Config) { c.AppleBundleIDs = []string{"com.a", ""} }, "AppleBundleIDs")
	bad(func(c *accountkit.Config) { c.AppleBundleIDs = []string{"com.a", "com.a"} }, "duplicate")
	bad(func(c *accountkit.Config) { c.AppleNonceTTL = 30 * time.Second }, "AppleNonceTTL")
	bad(func(c *accountkit.Config) { c.WeChatAPIBaseURL = "api.weixin.qq.com" }, "WeChatAPIBaseURL")
	bad(func(c *accountkit.Config) { c.AppleJWKSURL = "ftp://x/keys" }, "AppleJWKSURL")

	ok := minimal()
	ok.WeChatApps = []accountkit.WeChatApp{{AppID: "wx1", Secret: "s3cret"}, {AppID: "wx2", Secret: "t0p"}}
	ok.AppleBundleIDs = []string{"co.shifang.zavelo", "co.shifang.diet"}
	ok.WeChatAPIBaseURL = "http://127.0.0.1:9/" // 测试钩子允许 http
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseWeChatApps(t *testing.T) {
	apps, err := accountkit.ParseWeChatApps(" wx1:s1 , wx2:s2:with:colons ")
	if err != nil || len(apps) != 2 || apps[0] != (accountkit.WeChatApp{AppID: "wx1", Secret: "s1"}) || apps[1] != (accountkit.WeChatApp{AppID: "wx2", Secret: "s2:with:colons"}) {
		t.Fatalf("%+v %v", apps, err)
	}
	if apps, err := accountkit.ParseWeChatApps(""); err != nil || apps != nil {
		t.Fatal("empty spec → nil, nil")
	}
	for _, bad := range []string{"wx1", ":s1", "wx1:", "wx1:s1,"} {
		if _, err := accountkit.ParseWeChatApps(bad); err == nil {
			t.Fatalf("%q must fail", bad)
		} else if strings.Contains(err.Error(), "s1") {
			t.Fatalf("secret leaked: %v", err)
		}
	}
}

func TestConfigFromEnvIdP(t *testing.T) {
	t.Setenv("T_WECHAT_APPS", "wx1:s1,wx2:s2")
	t.Setenv("T_APPLE_BUNDLE_IDS", "co.shifang.zavelo, co.shifang.diet")
	t.Setenv("T_APPLE_NONCE_TTL", "15m")
	t.Setenv("T_WECHAT_API_BASE_URL", "http://127.0.0.1:1/")
	t.Setenv("T_APPLE_JWKS_URL", "http://127.0.0.1:2/keys")
	t.Setenv("T_JWT_KEYS", "1:"+b64(k(1)))
	t.Setenv("T_JWT_ACTIVE_KEY", "1")
	t.Setenv("T_JWT_ISSUER", "shifang")
	t.Setenv("T_JWT_AUDIENCE", "app")
	t.Setenv("T_SUBJECT_HMAC_KEYS", "1:"+b64(k(2)))
	t.Setenv("T_SUBJECT_HMAC_ACTIVE_KEY", "1")
	t.Setenv("T_SUBJECT_CIPHER_KEYS", "1:"+b64(k(3)))
	t.Setenv("T_SUBJECT_CIPHER_ACTIVE_KEY", "1")
	c, err := accountkit.ConfigFromEnv("T_")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.WeChatApps) != 2 || c.WeChatApps[1].AppID != "wx2" || len(c.AppleBundleIDs) != 2 || c.AppleBundleIDs[1] != "co.shifang.diet" || c.AppleNonceTTL != 15*time.Minute || c.WeChatAPIBaseURL != "http://127.0.0.1:1/" || c.AppleJWKSURL != "http://127.0.0.1:2/keys" {
		t.Fatalf("%+v", c)
	}
	t.Setenv("T_WECHAT_APPS", "broken")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil {
		t.Fatal("bad WECHAT_APPS must fail")
	}
}

func TestFacebookCannotBeOIDCName(t *testing.T) {
	c := minimal()
	c.OIDCProviders = []accountkit.OIDCProvider{{Kind: enum.IdentityFacebook, Issuer: "https://i.example", JWKSURL: "https://i.example/k", ClientIDs: []string{"c"}}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Fatalf("OIDC FACEBOOK: %v", err)
	}
}
