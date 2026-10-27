package accountkit

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// OIDCProvider 是一个按配置启用的 OIDC 身份提供方。Kind 须符合身份类型格式且不得为内置类型名；
// Issuer 与 JWKSURL 为绝对 http(s) URL，Issuer 与 ID token 的 iss 精确比较；ClientIDs 非空。
// NameClaims/PictureClaims 为 nil 时默认 ["name"]/["picture"]，非 nil 空切片表示不读取。
type OIDCProvider struct {
	Kind          enum.IdentityKind
	Issuer        string
	JWKSURL       string
	ClientIDs     []string
	NonceTTL      time.Duration // 默认 10m，须 >= 1m
	NameClaims    []string
	PictureClaims []string
}

// WeChatSubjectMode 决定实例内所有微信应用的固定身份识别方式。
type WeChatSubjectMode = idp.WeChatSubjectMode

// AlipayApp 是普通公钥模式的支付宝应用配置。
type AlipayApp = idp.AlipayApp

// AlipaySubjectMode 决定实例内所有支付宝应用的固定身份识别方式。
type AlipaySubjectMode = idp.AlipaySubjectMode

const (
	AlipaySubjectModeUnionID = idp.AlipaySubjectModeUnionID
	AlipaySubjectModeOpenID  = idp.AlipaySubjectModeOpenID
)

// QQSubjectMode 决定实例内所有 QQ 应用的固定身份识别方式。
type QQSubjectMode = idp.QQSubjectMode

const (
	QQSubjectModeUnionID = idp.QQSubjectModeUnionID
	QQSubjectModeOpenID  = idp.QQSubjectModeOpenID
)

const (
	WeChatSubjectModeUnionID = idp.WeChatSubjectModeUnionID
	WeChatSubjectModeOpenID  = idp.WeChatSubjectModeOpenID
)

// WeChatApp 是一个微信应用（小程序/App/公众号）的凭据；Secret 只在服务端换取 token 时使用。
type WeChatApp struct {
	AppID  string
	Secret string
}

// Config 是 accountkit 的全部配置。New 应用默认值并校验；Validate 可独立提前校验。
// 密钥只来自配置：JWT/HMAC/加密三组都是"版本 → 密钥"映射加一个 active 版本。
// JWT 签名密钥为 P-256 私钥（ES256），公钥经 JWKSHandler 发布。
type Config struct {
	// ProfileAttributes 为 nil 时关闭用户资料属性；启用后内置 HTTP 允许本人完整读写。
	// 宿主负责资料用途及 Go 直接调用授权，不应存放本人不可见或不可改的字段。
	ProfileAttributes *user.ProfileAttributesConfig
	// nil 使用默认策略，非 nil 空切片表示显式空列表。
	EnabledIdentityKinds  []enum.IdentityKind
	RequiredIdentityKinds []enum.IdentityKind
	// Schema 是所有表所在的 PostgreSQL schema。默认 "account"。
	Schema string
	// KeyPrefix 是所有 Redis 键的前缀，须以 ':' 结尾。默认 "auth:"。
	KeyPrefix string
	// AuditSource 默认 accountkit/<schema>；汇聚到外部记录器时必须显式配置。
	AuditSource string
	// AuditNamespace 默认 accountkit/<schema>/resources，持久化后变更须迁移。
	AuditNamespace string

	JWTKeys      map[uint16]*ecdsa.PrivateKey
	JWTActiveKey uint16
	JWTIssuer    string
	JWTAudience  string

	AccessTokenTTL  time.Duration // 默认 15m
	RefreshTokenTTL time.Duration // 默认 720h（30 天，滑动）
	RefreshGrace    time.Duration // 默认 30s
	ReauthMaxAge    time.Duration // 默认 5m
	// SensitiveOpVerification 为 nil 时视为 true。
	SensitiveOpVerification *bool
	// ProviderProfileBackfill 为 nil 时视为 true：已有账号在第三方登录、绑定或补全关联时
	// 用本次资料声明补填空的显示名与头像。新建账号始终填充，不受此项影响。
	ProviderProfileBackfill *bool

	SubjectHMACKeys        map[uint16][]byte
	SubjectHMACActiveKey   uint16
	SubjectCipherKeys      map[uint16][]byte
	SubjectCipherActiveKey uint16

	CodeTTL                   time.Duration // 默认 5m
	CodeMaxAttempts           int           // 默认 5
	CodeCooldown              time.Duration // 默认 60s
	CodeDailyLimitPerTarget   int           // 默认 10
	CodeDailyLimitPerIP       int           // 默认 100
	CodeFailureLimitPerTarget int           // 默认 10，跨用途累计错误预算
	CodeFailureWindow         time.Duration // 默认 15m，从首次错误起固定窗口
	MaxIdentitiesPerKind      int           // 默认 1

	DeletionCoolingPeriod time.Duration // 默认 360h（15 天）
	// PurgedUserRetention 是已清理用户记录（purged_user）的保留期：0 表示长期保留（默认）；
	// 大于 0 时不得短于 720h，由维护任务删除超出保留期的记录，外部业务服务须在保留期内完成拉取。
	PurgedUserRetention time.Duration
	AuditRetentionDays  int    // 默认 180
	DefaultRegion       string // 默认 "CN"

	// WeChatApps 配置 OAuth 应用；两组均为空时未启用微信登录。
	WeChatApps []WeChatApp
	// WeChatMiniProgramApps 配置通过 jscode2session 验证的小程序。
	WeChatMiniProgramApps []WeChatApp
	// WeChatSubjectMode 默认为 UNIONID，实例所有微信应用使用同一固定模式。
	WeChatSubjectMode WeChatSubjectMode
	// AppleBundleIDs 为空表示未启用 Apple 登录；id_token 的 aud 须在其中。
	AppleBundleIDs []string
	AppleNonceTTL  time.Duration // 默认 10m
	// GoogleClientIDs 为空表示未启用 Google 登录；id_token 的 aud 以及出现的 azp 须在其中。
	GoogleClientIDs []string
	GoogleNonceTTL  time.Duration // 默认 10m
	// FacebookAppID 为空表示未启用 Facebook 登录。非空时 FacebookAppSecret 必填：Classic 校验与
	// 数据删除回调的 signed_request 都依赖它。只对接一个 App，subject 为该 App 下的用户 id。
	FacebookAppID           string
	FacebookAppSecret       string
	FacebookGraphAPIVersion string        // 默认 idp.FacebookGraphAPIVersionDefault，格式 vN.N
	FacebookNonceTTL        time.Duration // 默认 10m
	// AlipayApps 为空表示未启用支付宝登录；每项使用独立的应用私钥及支付宝公钥。
	AlipayApps        []AlipayApp
	AlipaySubjectMode AlipaySubjectMode // 默认 UNIONID，所有应用固定使用同一模式。
	AlipayGatewayURL  string            // 默认生产网关；HTTP 仅允许回环测试地址。
	// QQAppIDs 为空表示未启用 QQ 登录；App SDK 校验 access_token 不需要 AppKey。
	QQAppIDs      []string
	QQSubjectMode QQSubjectMode // 默认 UNIONID，所有应用固定使用同一模式。
	QQAPIBaseURL  string        // 默认 https://graph.qq.com；HTTP 仅允许回环测试地址。
	// WeiboAppIDs 是允许的微博 AppKey，为空表示未启用；App SDK 校验 access_token 不需要 AppSecret。
	WeiboAppIDs     []string
	WeiboAPIBaseURL string // 默认 https://api.weibo.com；HTTP 仅允许回环测试地址。
	// OIDCProviders 是按配置启用的 OIDC 提供方，类型名一经使用不应更换。
	OIDCProviders []OIDCProvider
	// WeChatAPIBaseURL / AppleJWKSURL / GoogleJWKSURL / Facebook* 供测试与代理部署覆盖；生产保持默认。
	WeChatAPIBaseURL        string
	AppleJWKSURL            string
	GoogleJWKSURL           string
	FacebookGraphAPIBaseURL string
	FacebookJWKSURL         string

	MaintenanceInterval time.Duration // 默认 5m
}

var facebookVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+$`)

var alipayAppIDPattern = regexp.MustCompile(`^[0-9]{16}$`)
var qqAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
var weiboAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

func cloneProfileAttributesConfig(c *user.ProfileAttributesConfig) *user.ProfileAttributesConfig {
	if c == nil {
		return nil
	}
	snapshot := *c
	snapshot.Schema = slices.Clone(c.Schema)
	return &snapshot
}

// providerKinds 返回配置启用的第三方类型，顺序与 identityProviders 的装配一致。
func (c Config) providerKinds() []enum.IdentityKind {
	var kinds []enum.IdentityKind
	if len(c.WeChatApps) > 0 || len(c.WeChatMiniProgramApps) > 0 {
		kinds = append(kinds, enum.IdentityWeChat)
	}
	if len(c.AppleBundleIDs) > 0 {
		kinds = append(kinds, enum.IdentityApple)
	}
	if len(c.GoogleClientIDs) > 0 {
		kinds = append(kinds, enum.IdentityGoogle)
	}
	if c.FacebookAppID != "" {
		kinds = append(kinds, enum.IdentityFacebook)
	}
	if len(c.AlipayApps) > 0 {
		kinds = append(kinds, enum.IdentityAlipay)
	}
	if len(c.QQAppIDs) > 0 {
		kinds = append(kinds, enum.IdentityQQ)
	}
	if len(c.WeiboAppIDs) > 0 {
		kinds = append(kinds, enum.IdentityWeibo)
	}
	for _, p := range c.OIDCProviders {
		kinds = append(kinds, p.Kind)
	}
	return kinds
}

func (c Config) identityPolicy() (*user.IdentityPolicy, error) {
	return user.NewIdentityPolicy(c.EnabledIdentityKinds, c.RequiredIdentityKinds, c.providerKinds()...)
}

func boolPtr(b bool) *bool { return &b }

func (c Config) alipayConfig() idp.AlipayConfig {
	return idp.AlipayConfig{Apps: slices.Clone(c.AlipayApps), SubjectMode: c.AlipaySubjectMode, GatewayURL: c.AlipayGatewayURL}
}

func (c Config) qqConfig() idp.QQConfig {
	return idp.QQConfig{AppIDs: slices.Clone(c.QQAppIDs), SubjectMode: c.QQSubjectMode, APIBaseURL: c.QQAPIBaseURL}
}

func (c Config) weiboConfig() idp.WeiboConfig {
	return idp.WeiboConfig{AppIDs: slices.Clone(c.WeiboAppIDs), APIBaseURL: c.WeiboAPIBaseURL}
}

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
	if c.AuditSource == "" {
		c.AuditSource = "accountkit/" + c.Schema
	}
	if c.AuditNamespace == "" {
		c.AuditNamespace = "accountkit/" + c.Schema + "/resources"
	}
	def(&c.AccessTokenTTL, 15*time.Minute)
	def(&c.RefreshTokenTTL, 720*time.Hour)
	def(&c.RefreshGrace, 30*time.Second)
	def(&c.ReauthMaxAge, 5*time.Minute)
	if c.SensitiveOpVerification == nil {
		c.SensitiveOpVerification = boolPtr(true)
	}
	if c.ProviderProfileBackfill == nil {
		c.ProviderProfileBackfill = boolPtr(true)
	}
	def(&c.CodeTTL, 5*time.Minute)
	defi(&c.CodeMaxAttempts, 5)
	def(&c.CodeCooldown, 60*time.Second)
	defi(&c.CodeDailyLimitPerTarget, 10)
	defi(&c.CodeDailyLimitPerIP, 100)
	defi(&c.CodeFailureLimitPerTarget, 10)
	def(&c.CodeFailureWindow, 15*time.Minute)
	defi(&c.MaxIdentitiesPerKind, 1)
	def(&c.DeletionCoolingPeriod, 360*time.Hour)
	defi(&c.AuditRetentionDays, 180)
	if c.DefaultRegion == "" {
		c.DefaultRegion = "CN"
	}
	c.DefaultRegion = strings.ToUpper(c.DefaultRegion)
	c.AlipayApps = slices.Clone(c.AlipayApps)
	if c.AlipaySubjectMode == "" {
		c.AlipaySubjectMode = AlipaySubjectModeUnionID
	}
	if c.AlipayGatewayURL == "" {
		c.AlipayGatewayURL = "https://openapi.alipay.com/gateway.do"
	}
	c.QQAppIDs = slices.Clone(c.QQAppIDs)
	if c.QQSubjectMode == "" {
		c.QQSubjectMode = QQSubjectModeUnionID
	}
	if c.QQAPIBaseURL == "" {
		c.QQAPIBaseURL = "https://graph.qq.com"
	}
	c.WeiboAppIDs = slices.Clone(c.WeiboAppIDs)
	if c.WeiboAPIBaseURL == "" {
		c.WeiboAPIBaseURL = "https://api.weibo.com"
	}
	def(&c.AppleNonceTTL, 10*time.Minute)
	def(&c.GoogleNonceTTL, 10*time.Minute)
	def(&c.FacebookNonceTTL, 10*time.Minute)
	if c.FacebookGraphAPIVersion == "" {
		c.FacebookGraphAPIVersion = idp.FacebookGraphAPIVersionDefault
	}
	if c.FacebookGraphAPIBaseURL == "" {
		c.FacebookGraphAPIBaseURL = "https://graph.facebook.com"
	}
	if c.FacebookJWKSURL == "" {
		c.FacebookJWKSURL = "https://www.facebook.com/.well-known/oauth/openid/jwks/"
	}
	// 复制后再填默认值，避免写回宿主持有的切片。
	c.OIDCProviders = slices.Clone(c.OIDCProviders)
	for i := range c.OIDCProviders {
		def(&c.OIDCProviders[i].NonceTTL, 10*time.Minute)
	}
	if c.WeChatSubjectMode == "" {
		c.WeChatSubjectMode = WeChatSubjectModeUnionID
	}
	if c.WeChatAPIBaseURL == "" {
		c.WeChatAPIBaseURL = "https://api.weixin.qq.com"
	}
	if c.AppleJWKSURL == "" {
		c.AppleJWKSURL = "https://appleid.apple.com/auth/keys"
	}
	if c.GoogleJWKSURL == "" {
		c.GoogleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	}
	def(&c.MaintenanceInterval, 5*time.Minute)
}

// Validate 应用默认值后校验配置。宿主可在启动早期调用以尽早失败。
func (c Config) Validate() error {
	if err := c.validateBase(); err != nil {
		return err
	}
	return user.ValidateProfileAttributesConfig(c.ProfileAttributes)
}

func (c Config) validateBase() error {
	c.applyDefaults()
	if err := idp.ValidateAlipayConfig(c.alipayConfig()); err != nil {
		return fmt.Errorf("accountkit: %w", err)
	}
	if err := idp.ValidateQQConfig(c.qqConfig()); err != nil {
		return fmt.Errorf("accountkit: %w", err)
	}
	if err := idp.ValidateWeiboConfig(c.weiboConfig()); err != nil {
		return fmt.Errorf("accountkit: %w", err)
	}
	kinds := map[enum.IdentityKind]bool{}
	for i, p := range c.OIDCProviders {
		if field, err := validateOIDCProvider(p); err != nil {
			return fmt.Errorf("accountkit: OIDCProviders[%d].%s: %w", i, field, err)
		}
		if kinds[p.Kind] {
			return fmt.Errorf("accountkit: OIDCProviders[%d].Kind: duplicate %s", i, p.Kind)
		}
		kinds[p.Kind] = true
	}
	if _, err := c.identityPolicy(); err != nil {
		return err
	}
	if !migrations.ValidSchema(c.Schema) {
		return fmt.Errorf("accountkit: Schema %q must match ^[a-z][a-z0-9_]{0,62}$", c.Schema)
	}
	if !strings.HasSuffix(c.KeyPrefix, ":") || len(c.KeyPrefix) < 2 {
		return fmt.Errorf("accountkit: KeyPrefix %q must be non-empty and end with ':'", c.KeyPrefix)
	}
	if err := (authentication.Options{Source: c.AuditSource, Namespace: c.AuditNamespace}).Validate(); err != nil {
		return fmt.Errorf("accountkit: audit configuration: %w", err)
	}
	if err := tokens.CheckKeys(c.JWTKeys, c.JWTActiveKey); err != nil {
		return fmt.Errorf("accountkit: JWTKeys: %w", err)
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
	if c.CodeMaxAttempts <= 0 || c.CodeDailyLimitPerTarget <= 0 || c.CodeDailyLimitPerIP <= 0 || c.CodeFailureLimitPerTarget <= 0 {
		return errors.New("accountkit: Code* settings must be positive")
	}
	if c.CodeFailureWindow < time.Second {
		return errors.New("accountkit: CodeFailureWindow must be >= 1s")
	}
	if c.MaxIdentitiesPerKind <= 0 {
		return errors.New("accountkit: MaxIdentitiesPerKind must be >= 1")
	}
	if c.PurgedUserRetention < 0 || (c.PurgedUserRetention > 0 && c.PurgedUserRetention < user.MinPurgedUserRetention) {
		return fmt.Errorf("accountkit: PurgedUserRetention must be 0 (keep forever) or at least %s", user.MinPurgedUserRetention)
	}
	if c.DeletionCoolingPeriod <= 0 || c.AuditRetentionDays <= 0 {
		return errors.New("accountkit: DeletionCoolingPeriod and AuditRetentionDays must be positive")
	}
	if len(c.DefaultRegion) != 2 {
		return fmt.Errorf("accountkit: DefaultRegion %q must be an ISO 3166-1 alpha-2 code", c.DefaultRegion)
	}
	if c.WeChatSubjectMode != WeChatSubjectModeUnionID && c.WeChatSubjectMode != WeChatSubjectModeOpenID {
		return errors.New("accountkit: WeChatSubjectMode must be UNIONID or OPENID")
	}
	seen := map[string]struct{}{}
	for _, group := range []struct {
		name string
		apps []WeChatApp
	}{{"WeChatApps", c.WeChatApps}, {"WeChatMiniProgramApps", c.WeChatMiniProgramApps}} {
		for i, a := range group.apps {
			if strings.TrimSpace(a.AppID) == "" || strings.TrimSpace(a.Secret) == "" || strings.Contains(a.AppID, ":") {
				return fmt.Errorf("accountkit: %s[%d]: valid app_id and secret are required", group.name, i)
			}
			if _, dup := seen[a.AppID]; dup {
				return fmt.Errorf("accountkit: %s: duplicate app_id", group.name)
			}
			seen[a.AppID] = struct{}{}
		}
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
	seenG := map[string]struct{}{}
	for i, id := range c.GoogleClientIDs {
		if id == "" {
			return fmt.Errorf("accountkit: GoogleClientIDs[%d] must not be empty", i)
		}
		if _, dup := seenG[id]; dup {
			return fmt.Errorf("accountkit: GoogleClientIDs: duplicate %q", id)
		}
		seenG[id] = struct{}{}
	}
	if c.GoogleNonceTTL < time.Minute {
		return errors.New("accountkit: GoogleNonceTTL must be >= 1m")
	}
	if c.FacebookAppID != "" && c.FacebookAppSecret == "" {
		return errors.New("accountkit: FacebookAppSecret is required when FacebookAppID is set")
	}
	if c.FacebookAppID != "" && !facebookVersionPattern.MatchString(c.FacebookGraphAPIVersion) {
		return errors.New(`accountkit: FacebookGraphAPIVersion must match ^v[0-9]+\.[0-9]+$`)
	}
	if c.FacebookAppID != "" && c.FacebookNonceTTL < time.Minute {
		return errors.New("accountkit: FacebookNonceTTL must be >= 1m")
	}
	for name, raw := range map[string]string{"WeChatAPIBaseURL": c.WeChatAPIBaseURL, "AppleJWKSURL": c.AppleJWKSURL, "GoogleJWKSURL": c.GoogleJWKSURL,
		"FacebookGraphAPIBaseURL": c.FacebookGraphAPIBaseURL, "FacebookJWKSURL": c.FacebookJWKSURL} {
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
// AuditSource/AuditNamespace 保留显式输入的空/非空状态，默认值延迟到 New 装配；
// Validate 对其本地默认值做校验但不填回。其他既有字段仍在返回前应用默认值。
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

	kinds := func(name string, dst *[]enum.IdentityKind) {
		raw, ok := os.LookupEnv(prefix + name)
		if !ok {
			return
		}
		raw = strings.TrimSpace(raw)
		if raw == "[]" {
			*dst = []enum.IdentityKind{}
			return
		}
		if raw == "" {
			fail(name, errors.New("empty value; use [] for an explicit empty list"))
			return
		}
		values := []enum.IdentityKind{}
		seen := map[enum.IdentityKind]bool{}
		for _, v := range strings.Split(raw, ",") {
			k, err := enum.ParseIdentityKind(strings.TrimSpace(v))
			if err != nil {
				fail(name, err)
				return
			}
			if seen[k] {
				fail(name, errors.New("duplicate identity kind"))
				return
			}
			seen[k] = true
			values = append(values, k)
		}
		*dst = values
	}
	kinds("ENABLED_IDENTITY_KINDS", &c.EnabledIdentityKinds)
	kinds("REQUIRED_IDENTITY_KINDS", &c.RequiredIdentityKinds)
	c.Schema = str("AUTH_SCHEMA")
	c.KeyPrefix = str("AUTH_KEY_PREFIX")
	c.AuditSource = str("AUDIT_SOURCE")
	c.AuditNamespace = str("AUDIT_NAMESPACE")
	if s := str("JWT_KEYS"); s != "" {
		if m, err := tokens.ParseKeyList(s); err != nil {
			fail("JWT_KEYS", err)
		} else {
			c.JWTKeys = m
		}
	}
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
	if s := str("PROVIDER_PROFILE_BACKFILL"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			fail("PROVIDER_PROFILE_BACKFILL", err)
		} else {
			c.ProviderProfileBackfill = boolPtr(b)
		}
	}
	keylist("SUBJECT_HMAC_KEYS", &c.SubjectHMACKeys)
	u16("SUBJECT_HMAC_ACTIVE_KEY", &c.SubjectHMACActiveKey)
	keylist("SUBJECT_CIPHER_KEYS", &c.SubjectCipherKeys)
	u16("SUBJECT_CIPHER_ACTIVE_KEY", &c.SubjectCipherActiveKey)
	dur("CODE_TTL", &c.CodeTTL)
	dur("PURGED_USER_RETENTION", &c.PurgedUserRetention)
	integer("CODE_MAX_ATTEMPTS", &c.CodeMaxAttempts)
	dur("CODE_COOLDOWN", &c.CodeCooldown)
	integer("CODE_DAILY_LIMIT_PER_TARGET", &c.CodeDailyLimitPerTarget)
	integer("CODE_DAILY_LIMIT_PER_IP", &c.CodeDailyLimitPerIP)
	integer("CODE_FAILURE_LIMIT_PER_TARGET", &c.CodeFailureLimitPerTarget)
	dur("CODE_FAILURE_WINDOW", &c.CodeFailureWindow)
	integer("MAX_IDENTITIES_PER_KIND", &c.MaxIdentitiesPerKind)
	dur("DELETION_COOLING_PERIOD", &c.DeletionCoolingPeriod)
	integer("AUDIT_RETENTION_DAYS", &c.AuditRetentionDays)
	c.DefaultRegion = str("DEFAULT_REGION")
	dur("MAINTENANCE_INTERVAL", &c.MaintenanceInterval)
	for _, group := range []struct {
		name string
		dst  *[]WeChatApp
	}{{"WECHAT_APPS", &c.WeChatApps}, {"WECHAT_MINI_PROGRAM_APPS", &c.WeChatMiniProgramApps}} {
		if raw := str(group.name); raw != "" {
			apps, err := ParseWeChatApps(raw)
			if err != nil {
				fail(group.name, err)
			} else {
				*group.dst = apps
			}
		}
	}
	if raw, ok := os.LookupEnv(prefix + "WECHAT_SUBJECT_MODE"); ok {
		c.WeChatSubjectMode = WeChatSubjectMode(raw)
		if c.WeChatSubjectMode != WeChatSubjectModeUnionID && c.WeChatSubjectMode != WeChatSubjectModeOpenID {
			fail("WECHAT_SUBJECT_MODE", errors.New("must be UNIONID or OPENID"))
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
	if s := str("GOOGLE_CLIENT_IDS"); s != "" {
		for _, id := range strings.Split(s, ",") {
			if id = strings.TrimSpace(id); id != "" {
				c.GoogleClientIDs = append(c.GoogleClientIDs, id)
			}
		}
	}
	dur("GOOGLE_NONCE_TTL", &c.GoogleNonceTTL)
	c.WeChatAPIBaseURL = str("WECHAT_API_BASE_URL")
	c.AppleJWKSURL = str("APPLE_JWKS_URL")
	c.GoogleJWKSURL = str("GOOGLE_JWKS_URL")
	c.FacebookAppID = str("FACEBOOK_APP_ID")
	c.FacebookAppSecret = str("FACEBOOK_APP_SECRET")
	c.FacebookGraphAPIVersion = str("FACEBOOK_GRAPH_API_VERSION")
	dur("FACEBOOK_NONCE_TTL", &c.FacebookNonceTTL)
	c.FacebookGraphAPIBaseURL = str("FACEBOOK_GRAPH_API_BASE_URL")
	c.FacebookJWKSURL = str("FACEBOOK_JWKS_URL")
	if raw, ok := os.LookupEnv(prefix + "ALIPAY_SUBJECT_MODE"); ok {
		c.AlipaySubjectMode = AlipaySubjectMode(raw)
		if c.AlipaySubjectMode != AlipaySubjectModeUnionID && c.AlipaySubjectMode != AlipaySubjectModeOpenID {
			fail("ALIPAY_SUBJECT_MODE", errors.New("must be UNIONID or OPENID"))
		}
	}
	c.AlipayGatewayURL = str("ALIPAY_GATEWAY_URL")
	if raw := str("ALIPAY_APP_IDS"); raw != "" {
		seen := map[string]bool{}
		for _, item := range strings.Split(raw, ",") {
			appID := strings.TrimSpace(item)
			if !alipayAppIDPattern.MatchString(appID) || seen[appID] {
				fail("ALIPAY_APP_IDS", errors.New("entries must be unique 16-digit ASCII AppIDs"))
				break
			}
			seen[appID] = true
			base := "ALIPAY_" + appID + "_"
			c.AlipayApps = append(c.AlipayApps, AlipayApp{AppID: appID, PrivateKey: os.Getenv(prefix + base + "PRIVATE_KEY"), AlipayPublicKey: os.Getenv(prefix + base + "PUBLIC_KEY")})
		}
	}
	if raw, ok := os.LookupEnv(prefix + "QQ_SUBJECT_MODE"); ok {
		c.QQSubjectMode = QQSubjectMode(raw)
		if c.QQSubjectMode != QQSubjectModeUnionID && c.QQSubjectMode != QQSubjectModeOpenID {
			fail("QQ_SUBJECT_MODE", errors.New("must be UNIONID or OPENID"))
		}
	}
	c.QQAPIBaseURL = str("QQ_API_BASE_URL")
	if raw := str("QQ_APP_IDS"); raw != "" {
		seen := map[string]bool{}
		for _, item := range strings.Split(raw, ",") {
			appID := strings.TrimSpace(item)
			if !qqAppIDPattern.MatchString(appID) || seen[appID] {
				fail("QQ_APP_IDS", errors.New("entries must be unique AppIDs of 1-20 ASCII digits"))
				break
			}
			seen[appID] = true
			c.QQAppIDs = append(c.QQAppIDs, appID)
		}
	}
	c.WeiboAPIBaseURL = str("WEIBO_API_BASE_URL")
	if raw := str("WEIBO_APP_IDS"); raw != "" {
		seen := map[string]bool{}
		for _, item := range strings.Split(raw, ",") {
			appID := strings.TrimSpace(item)
			if !weiboAppIDPattern.MatchString(appID) || seen[appID] {
				fail("WEIBO_APP_IDS", errors.New("entries must be unique AppKeys of 1-20 ASCII digits"))
				break
			}
			seen[appID] = true
			c.WeiboAppIDs = append(c.WeiboAppIDs, appID)
		}
	}
	if s := str("OIDC_PROVIDERS"); s != "" {
		c.OIDCProviders = oidcProvidersFromEnv(s, str, dur, fail, func(name string) (string, bool) { return os.LookupEnv(prefix + name) })
	}

	if firstErr != nil {
		return Config{}, firstErr
	}
	// 外部记录器需要区分未配置和显式配置相同默认字符串。
	auditSource, auditNamespace := c.AuditSource, c.AuditNamespace
	c.applyDefaults()
	c.AuditSource, c.AuditNamespace = auditSource, auditNamespace
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validateOIDCProvider 返回出错的字段名，供 Validate 报字段、ConfigFromEnv 报变量名。
// NonceTTL 为零表示使用默认值。
func validateOIDCProvider(p OIDCProvider) (string, error) {
	if !p.Kind.Valid() {
		return "Kind", errors.New("must match ^[A-Z][A-Z0-9_]{1,31}$")
	}
	if p.Kind.IsBuiltin() {
		return "Kind", fmt.Errorf("%s is a builtin identity kind", p.Kind)
	}
	for _, f := range []struct{ field, raw string }{{"Issuer", p.Issuer}, {"JWKSURL", p.JWKSURL}} {
		field, raw := f.field, f.raw
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return field, errors.New("must be an absolute http(s) URL")
		}
	}
	if len(p.ClientIDs) == 0 {
		return "ClientIDs", errors.New("at least one client ID is required")
	}
	for _, f := range []struct {
		field string
		list  []string
	}{{"ClientIDs", p.ClientIDs}, {"NameClaims", p.NameClaims}, {"PictureClaims", p.PictureClaims}} {
		field, list := f.field, f.list
		seen := map[string]bool{}
		for _, v := range list {
			if v == "" || seen[v] {
				return field, errors.New("entries must be non-empty and unique")
			}
			seen[v] = true
		}
	}
	if p.NonceTTL != 0 && p.NonceTTL < time.Minute {
		return "NonceTTL", errors.New("must be >= 1m")
	}
	return "", nil
}

// oidcEnvSuffix 把 OIDCProvider 字段映射到 OIDC_<KIND>_ 之后的变量名后缀。
var oidcEnvSuffix = map[string]string{"Issuer": "ISSUER", "JWKSURL": "JWKS_URL", "ClientIDs": "CLIENT_IDS", "NonceTTL": "NONCE_TTL", "NameClaims": "NAME_CLAIMS", "PictureClaims": "PICTURE_CLAIMS"}

// oidcProvidersFromEnv 解析 OIDC_PROVIDERS 及每个类型的 OIDC_<KIND>_* 变量，错误指出具体变量名。
func oidcProvidersFromEnv(list string, str func(string) string, dur func(string, *time.Duration), fail func(string, error), lookup func(string) (string, bool)) []OIDCProvider {
	split := func(raw string) []string {
		out := []string{}
		for _, v := range strings.Split(raw, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	names := split(list)
	seen := map[enum.IdentityKind]bool{}
	for _, name := range names {
		kind := enum.IdentityKind(name)
		if !kind.Valid() || kind.IsBuiltin() || seen[kind] {
			fail("OIDC_PROVIDERS", errors.New("entries must be unique, non-builtin and match ^[A-Z][A-Z0-9_]{1,31}$"))
			return nil
		}
		seen[kind] = true
	}
	var providers []OIDCProvider
	for _, name := range names {
		kind := enum.IdentityKind(name)
		base := "OIDC_" + name + "_"
		p := OIDCProvider{Kind: kind, Issuer: str(base + "ISSUER"), JWKSURL: str(base + "JWKS_URL"), ClientIDs: split(str(base + "CLIENT_IDS"))}
		dur(base+"NONCE_TTL", &p.NonceTTL)
		if raw, ok := lookup(base + "NAME_CLAIMS"); ok {
			p.NameClaims = split(raw)
		}
		if raw, ok := lookup(base + "PICTURE_CLAIMS"); ok {
			p.PictureClaims = split(raw)
		}
		if field, err := validateOIDCProvider(p); err != nil {
			fail(base+oidcEnvSuffix[field], err)
			return nil
		}
		providers = append(providers, p)
	}
	return providers
}
