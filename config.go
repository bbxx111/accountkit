package accountkit

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/tokens"
)

// WeChatApp 是一个微信应用（小程序/App/公众号）的凭据；Secret 只在服务端换取 token 时使用。
type WeChatApp struct {
	AppID  string
	Secret string
}

// Config 是 accountkit 的全部配置。零值字段由 applyDefaults 填默认；Validate 在 New 中调用。
// 密钥只来自配置：JWT/HMAC/加密三组都是"版本 → 密钥"映射加一个 active 版本。
type Config struct {
	// Schema 是所有表所在的 PostgreSQL schema。默认 "account"。
	Schema string
	// KeyPrefix 是所有 Redis 键的前缀，须以 ':' 结尾。默认 "auth:"。
	KeyPrefix string

	JWTKeys      map[uint16][]byte
	JWTActiveKey uint16
	JWTIssuer    string
	JWTAudience  string

	AccessTokenTTL  time.Duration // 默认 15m
	RefreshTokenTTL time.Duration // 默认 720h（30 天，滑动）
	RefreshGrace    time.Duration // 默认 30s
	ReauthMaxAge    time.Duration // 默认 5m
	// SensitiveOpVerification 为 nil 时视为 true。
	SensitiveOpVerification *bool

	SubjectHMACKeys        map[uint16][]byte
	SubjectHMACActiveKey   uint16
	SubjectCipherKeys      map[uint16][]byte
	SubjectCipherActiveKey uint16

	CodeTTL                 time.Duration // 默认 5m
	CodeMaxAttempts         int           // 默认 5
	CodeCooldown            time.Duration // 默认 60s
	CodeDailyLimitPerTarget int           // 默认 10
	CodeDailyLimitPerIP     int           // 默认 100
	MaxIdentitiesPerKind    int           // 默认 1

	DeletionCoolingPeriod time.Duration // 默认 360h（15 天）
	AuditRetentionDays    int           // 默认 180
	DefaultRegion         string        // 默认 "CN"

	// WeChatApps 为空表示未启用微信登录。
	WeChatApps []WeChatApp
	// AppleBundleIDs 为空表示未启用 Apple 登录；id_token 的 aud 须在其中。
	AppleBundleIDs []string
	AppleNonceTTL  time.Duration // 默认 10m
	// WeChatAPIBaseURL / AppleJWKSURL 供测试与代理部署覆盖；生产保持默认。
	WeChatAPIBaseURL string
	AppleJWKSURL     string

	MaintenanceInterval time.Duration // 默认 5m
}

func boolPtr(b bool) *bool { return &b }

func (c *Config) applyDefaults() {
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	defi := func(i *int, v int) {
		if *i == 0 {
			*i = v
		}
	}
	if c.Schema == "" {
		c.Schema = "account"
	}
	if c.KeyPrefix == "" {
		c.KeyPrefix = "auth:"
	}
	def(&c.AccessTokenTTL, 15*time.Minute)
	def(&c.RefreshTokenTTL, 720*time.Hour)
	def(&c.RefreshGrace, 30*time.Second)
	def(&c.ReauthMaxAge, 5*time.Minute)
	if c.SensitiveOpVerification == nil {
		c.SensitiveOpVerification = boolPtr(true)
	}
	def(&c.CodeTTL, 5*time.Minute)
	defi(&c.CodeMaxAttempts, 5)
	def(&c.CodeCooldown, 60*time.Second)
	defi(&c.CodeDailyLimitPerTarget, 10)
	defi(&c.CodeDailyLimitPerIP, 100)
	defi(&c.MaxIdentitiesPerKind, 1)
	def(&c.DeletionCoolingPeriod, 360*time.Hour)
	defi(&c.AuditRetentionDays, 180)
	if c.DefaultRegion == "" {
		c.DefaultRegion = "CN"
	}
	c.DefaultRegion = strings.ToUpper(c.DefaultRegion)
	def(&c.AppleNonceTTL, 10*time.Minute)
	if c.WeChatAPIBaseURL == "" {
		c.WeChatAPIBaseURL = "https://api.weixin.qq.com"
	}
	if c.AppleJWKSURL == "" {
		c.AppleJWKSURL = "https://appleid.apple.com/auth/keys"
	}
	def(&c.MaintenanceInterval, 5*time.Minute)
}

// Validate 应用默认值后校验配置。宿主可在启动早期调用以尽早失败。
func (c Config) Validate() error {
	c.applyDefaults()
	if !migrations.ValidSchema(c.Schema) {
		return fmt.Errorf("accountkit: Schema %q must match ^[a-z][a-z0-9_]{0,62}$", c.Schema)
	}
	if !strings.HasSuffix(c.KeyPrefix, ":") || len(c.KeyPrefix) < 2 {
		return fmt.Errorf("accountkit: KeyPrefix %q must be non-empty and end with ':'", c.KeyPrefix)
	}
	if err := checkKeySet("JWTKeys", c.JWTKeys, c.JWTActiveKey, tokens.MinKeyLen, false); err != nil {
		return err
	}
	if c.JWTIssuer == "" || c.JWTAudience == "" {
		return errors.New("accountkit: JWTIssuer and JWTAudience are required")
	}
	if err := checkKeySet("SubjectHMACKeys", c.SubjectHMACKeys, c.SubjectHMACActiveKey, pii.MinKeyLen, false); err != nil {
		return err
	}
	// SubjectCipherKeys 供 pii.NewCipher（AES-256-GCM）使用，要求恰好 32 字节；
	// 提前在此拒绝，比在装配时才失败更早、更清楚。
	if err := checkKeySet("SubjectCipherKeys", c.SubjectCipherKeys, c.SubjectCipherActiveKey, pii.MinKeyLen, true); err != nil {
		return err
	}
	if c.AccessTokenTTL < time.Minute {
		return errors.New("accountkit: AccessTokenTTL must be >= 1m")
	}
	if c.RefreshTokenTTL <= c.AccessTokenTTL {
		return errors.New("accountkit: RefreshTokenTTL must be longer than AccessTokenTTL")
	}
	if c.RefreshGrace <= 0 || c.RefreshGrace >= c.AccessTokenTTL {
		return errors.New("accountkit: RefreshGrace must be > 0 and < AccessTokenTTL")
	}
	if c.ReauthMaxAge <= 0 {
		return errors.New("accountkit: ReauthMaxAge must be > 0")
	}
	// CodeTTL/CodeCooldown 在 code.Store 里按整秒传给 Redis 的 EX 选项；一个介于 0 和 1s
	// 之间的值会被截断成 EX 0，Redis 视为非法参数并报错，而不是"立即过期"。
	if c.CodeTTL < time.Second || c.CodeCooldown < time.Second {
		return errors.New("accountkit: CodeTTL and CodeCooldown must be >= 1s")
	}
	if c.CodeMaxAttempts <= 0 || c.CodeDailyLimitPerTarget <= 0 || c.CodeDailyLimitPerIP <= 0 {
		return errors.New("accountkit: Code* settings must be positive")
	}
	if c.MaxIdentitiesPerKind <= 0 {
		return errors.New("accountkit: MaxIdentitiesPerKind must be >= 1")
	}
	if c.DeletionCoolingPeriod <= 0 || c.AuditRetentionDays <= 0 {
		return errors.New("accountkit: DeletionCoolingPeriod and AuditRetentionDays must be positive")
	}
	if len(c.DefaultRegion) != 2 {
		return fmt.Errorf("accountkit: DefaultRegion %q must be an ISO 3166-1 alpha-2 code", c.DefaultRegion)
	}
	seen := map[string]struct{}{}
	for i, a := range c.WeChatApps {
		if a.AppID == "" || a.Secret == "" {
			return fmt.Errorf("accountkit: WeChatApps[%d]: app_id and secret are required", i)
		}
		if _, dup := seen[a.AppID]; dup {
			return fmt.Errorf("accountkit: WeChatApps: duplicate app_id %q", a.AppID)
		}
		seen[a.AppID] = struct{}{}
	}
	seenB := map[string]struct{}{}
	for i, b := range c.AppleBundleIDs {
		if b == "" {
			return fmt.Errorf("accountkit: AppleBundleIDs[%d] must not be empty", i)
		}
		if _, dup := seenB[b]; dup {
			return fmt.Errorf("accountkit: AppleBundleIDs: duplicate %q", b)
		}
		seenB[b] = struct{}{}
	}
	if c.AppleNonceTTL < time.Minute {
		return errors.New("accountkit: AppleNonceTTL must be >= 1m")
	}
	for name, raw := range map[string]string{"WeChatAPIBaseURL": c.WeChatAPIBaseURL, "AppleJWKSURL": c.AppleJWKSURL} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("accountkit: %s %q must be an absolute http(s) URL", name, raw)
		}
	}
	if c.MaintenanceInterval < time.Second {
		return errors.New("accountkit: MaintenanceInterval must be >= 1s")
	}
	return nil
}

// checkKeySet 校验非空、每个密钥长度（exact=true 时要求恰好 minLen 字节，否则要求
// 至少 minLen 字节）、以及 active 版本存在。
func checkKeySet(name string, keys map[uint16][]byte, active uint16, minLen int, exact bool) error {
	if len(keys) == 0 {
		return fmt.Errorf("accountkit: %s is required", name)
	}
	for v, k := range keys {
		if exact {
			if len(k) != minLen {
				return fmt.Errorf("accountkit: %s version %d is %d bytes, AES-256-GCM requires exactly %d", name, v, len(k), minLen)
			}
		} else if len(k) < minLen {
			return fmt.Errorf("accountkit: %s version %d is %d bytes, want at least %d", name, v, len(k), minLen)
		}
	}
	if _, ok := keys[active]; !ok {
		return fmt.Errorf("accountkit: %s active version %d is not configured", name, active)
	}
	return nil
}

// ParseWeChatApps 解析 "appid:secret,appid:secret"；secret 可含冒号（按第一个冒号切分）。空串 → nil。
func ParseWeChatApps(spec string) ([]WeChatApp, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var out []WeChatApp
	for i, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		id, secret, ok := strings.Cut(item, ":")
		id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
		if !ok || id == "" || secret == "" {
			return nil, fmt.Errorf("accountkit: WeChatApps item %d: expected app_id:secret", i)
		}
		out = append(out, WeChatApp{AppID: id, Secret: secret})
	}
	return out, nil
}

// ConfigFromEnv 从环境变量读取配置（变量名 = prefix + 名称，见 README）。
// 只解析与格式相关的错误在此报告（并点名变量）；语义校验由 Validate 完成。
func ConfigFromEnv(prefix string) (Config, error) {
	var c Config
	var firstErr error
	fail := func(name string, err error) {
		if firstErr == nil {
			firstErr = fmt.Errorf("accountkit: %s%s: %w", prefix, name, err)
		}
	}
	str := func(name string) string { return strings.TrimSpace(os.Getenv(prefix + name)) }
	dur := func(name string, dst *time.Duration) {
		if s := str(name); s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				fail(name, err)
				return
			}
			*dst = d
		}
	}
	integer := func(name string, dst *int) {
		if s := str(name); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				fail(name, err)
				return
			}
			*dst = n
		}
	}
	u16 := func(name string, dst *uint16) {
		if s := str(name); s != "" {
			n, err := strconv.ParseUint(s, 10, 16)
			if err != nil {
				fail(name, err)
				return
			}
			*dst = uint16(n)
		}
	}
	keylist := func(name string, dst *map[uint16][]byte) {
		if s := str(name); s != "" {
			m, err := pii.ParseKeyList(s)
			if err != nil {
				fail(name, err)
				return
			}
			*dst = m
		}
	}

	c.Schema = str("AUTH_SCHEMA")
	c.KeyPrefix = str("AUTH_KEY_PREFIX")
	keylist("JWT_KEYS", &c.JWTKeys)
	u16("JWT_ACTIVE_KEY", &c.JWTActiveKey)
	c.JWTIssuer = str("JWT_ISSUER")
	c.JWTAudience = str("JWT_AUDIENCE")
	dur("ACCESS_TOKEN_TTL", &c.AccessTokenTTL)
	dur("REFRESH_TOKEN_TTL", &c.RefreshTokenTTL)
	dur("REFRESH_GRACE", &c.RefreshGrace)
	dur("REAUTH_MAX_AGE", &c.ReauthMaxAge)
	if s := str("SENSITIVE_OP_VERIFICATION"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			fail("SENSITIVE_OP_VERIFICATION", err)
		} else {
			c.SensitiveOpVerification = boolPtr(b)
		}
	}
	keylist("SUBJECT_HMAC_KEYS", &c.SubjectHMACKeys)
	u16("SUBJECT_HMAC_ACTIVE_KEY", &c.SubjectHMACActiveKey)
	keylist("SUBJECT_CIPHER_KEYS", &c.SubjectCipherKeys)
	u16("SUBJECT_CIPHER_ACTIVE_KEY", &c.SubjectCipherActiveKey)
	dur("CODE_TTL", &c.CodeTTL)
	integer("CODE_MAX_ATTEMPTS", &c.CodeMaxAttempts)
	dur("CODE_COOLDOWN", &c.CodeCooldown)
	integer("CODE_DAILY_LIMIT_PER_TARGET", &c.CodeDailyLimitPerTarget)
	integer("CODE_DAILY_LIMIT_PER_IP", &c.CodeDailyLimitPerIP)
	integer("MAX_IDENTITIES_PER_KIND", &c.MaxIdentitiesPerKind)
	dur("DELETION_COOLING_PERIOD", &c.DeletionCoolingPeriod)
	integer("AUDIT_RETENTION_DAYS", &c.AuditRetentionDays)
	c.DefaultRegion = str("DEFAULT_REGION")
	dur("MAINTENANCE_INTERVAL", &c.MaintenanceInterval)
	if s := str("WECHAT_APPS"); s != "" {
		apps, err := ParseWeChatApps(s)
		if err != nil {
			fail("WECHAT_APPS", err)
		} else {
			c.WeChatApps = apps
		}
	}
	if s := str("APPLE_BUNDLE_IDS"); s != "" {
		for _, b := range strings.Split(s, ",") {
			if b = strings.TrimSpace(b); b != "" {
				c.AppleBundleIDs = append(c.AppleBundleIDs, b)
			}
		}
	}
	dur("APPLE_NONCE_TTL", &c.AppleNonceTTL)
	c.WeChatAPIBaseURL = str("WECHAT_API_BASE_URL")
	c.AppleJWKSURL = str("APPLE_JWKS_URL")

	if firstErr != nil {
		return Config{}, firstErr
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
