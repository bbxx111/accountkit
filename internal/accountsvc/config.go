// Package accountsvc 装配可选的独立账号服务；嵌入式宿主不依赖此包。
package accountsvc

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/aliyun"
	"github.com/bbxx111/accountkit/user/sender/smtp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Config 区分库配置和服务运行配置；其中的秘密不得整体写入日志。
type Config struct {
	Library                   accountkit.Config
	Mode                      string
	DatabaseURL               string
	RedisURL                  string
	HTTPAddr                  string
	TLSEnabled                bool // LoadConfig 解析后的有效 HTTP TLS 模式。
	TLSCertFile               string
	TLSKeyFile                string
	StartupTimeout            time.Duration
	ShutdownTimeout           time.Duration
	TrustedProxyCIDRs         []netip.Prefix
	SMTP                      smtp.Config
	SMS                       *aliyun.Config      // nil 表示未配置短信服务商
	SMTPBrand                 string              // 内置中文邮件的可选品牌前缀
	IntrospectionBasicClients map[string][]string // 与 Service.IntrospectionBearerClients 都为空时 /v1/introspect 返回 503 introspection_not_configured
	PurgeFeedBasicClients     map[string][]string // 与 Service.PurgeFeedBearerClients 都为空时 /v1/purgedUsers 返回 503 PURGE_FEED_NOT_CONFIGURED
	Service                   ServiceConfig
	AdminEnabled              bool
	Admin                     adminauth.Config
	MetricsEnabled            bool
	MetricsInstance           string
	command                   string
	tlsConfig                 *tls.Config
}

func invalidConfig(name string) error { return errors.New("accountsvc: invalid or missing " + name) }

// envPrefix 是 accountsvc 读取库配置与服务配置的唯一环境变量前缀。
const envPrefix = "ACCOUNTSVC_"

func env(name string) string { return strings.TrimSpace(os.Getenv(envPrefix + name)) }
func defaultEnv(name, value string) string {
	if s := env(name); s != "" {
		return s
	}
	return value
}
func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	s := env(name)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, invalidConfig("ACCOUNTSVC_" + name)
	}
	return d, nil
}

// rejectLegacyPrefix 拒绝非空的旧库配置前缀变量：改名后可选配置不能被静默忽略。
// 错误只含变量名，不回显值。
func rejectLegacyPrefix() error {
	var legacy []string
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if suffix, ok := strings.CutPrefix(name, "ACCOUNTKIT_"); ok && value != "" {
			legacy = append(legacy, name+" -> "+envPrefix+suffix)
		}
	}
	if len(legacy) == 0 {
		return nil
	}
	sort.Strings(legacy)
	return errors.New("accountsvc: ACCOUNTKIT_ variables are no longer read; rename " + strings.Join(legacy, ", "))
}

// LoadConfig 先校验配置再允许创建依赖；migrate 不读取服务专用设置。
func LoadConfig(command string) (Config, error) {
	if command != "serve" && command != "migrate" {
		return Config{}, errors.New("accountsvc: command must be serve or migrate")
	}
	if err := rejectLegacyPrefix(); err != nil {
		return Config{}, err
	}
	c := Config{command: command, ShutdownTimeout: 30 * time.Second}
	var err error
	c.Library, err = accountkit.ConfigFromEnv(envPrefix)
	if err != nil {
		return Config{}, safeLibraryConfigError(err)
	}
	c.DatabaseURL = env("DATABASE_URL")
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" {
		return Config{}, invalidConfig("ACCOUNTSVC_DATABASE_URL")
	}
	if _, err = pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return Config{}, invalidConfig("ACCOUNTSVC_DATABASE_URL")
	}
	c.RedisURL = env("REDIS_URL")
	u, err = url.Parse(c.RedisURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Fragment != "" {
		return Config{}, invalidConfig("ACCOUNTSVC_REDIS_URL")
	}
	if _, err = redis.ParseURL(c.RedisURL); err != nil {
		return Config{}, invalidConfig("ACCOUNTSVC_REDIS_URL")
	}
	c.StartupTimeout, err = durationEnv("STARTUP_TIMEOUT", time.Minute)
	if err != nil {
		return Config{}, err
	}
	if command == "migrate" {
		return c, nil
	}
	c.Library.ProfileAttributes, err = loadProfileAttributes(env("PROFILE_ATTRIBUTES_SCHEMA_FILE"))
	if err != nil {
		return Config{}, err
	}
	if raw := env("METRICS_ENABLED"); raw != "" {
		c.MetricsEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_METRICS_ENABLED")
		}
	}
	if c.MetricsEnabled {
		c.MetricsInstance = env("METRICS_INSTANCE")
		if !metricsInstancePattern.MatchString(c.MetricsInstance) {
			return Config{}, invalidConfig("ACCOUNTSVC_METRICS_INSTANCE")
		}
	}
	if c.SMS, err = loadSMS(defaultEnv("MODE", "production")); err != nil {
		return Config{}, err
	}
	if c.Library.EnabledIdentityKinds != nil {
		if c.identityEnabled(enum.IdentityPhone) && c.SMS == nil {
			return Config{}, errors.New("accountsvc: PHONE requires an SMS provider; set ACCOUNTSVC_SMS_PROVIDER (embedded hosts inject an SMS sender)")
		}
		if c.identityEnabled(enum.IdentityWeChat) && len(c.Library.WeChatApps) == 0 && len(c.Library.WeChatMiniProgramApps) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_WECHAT_APPS")
		}
		if c.identityEnabled(enum.IdentityApple) && len(c.Library.AppleBundleIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_APPLE_BUNDLE_IDS")
		}
		if c.identityEnabled(enum.IdentityGoogle) && len(c.Library.GoogleClientIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_GOOGLE_CLIENT_IDS")
		}
		if c.identityEnabled(enum.IdentityFacebook) && c.Library.FacebookAppID == "" {
			return Config{}, invalidConfig("ACCOUNTSVC_FACEBOOK_APP_ID")
		}
		if c.identityEnabled(enum.IdentityAlipay) && len(c.Library.AlipayApps) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_ALIPAY_APP_IDS")
		}
		if c.identityEnabled(enum.IdentityQQ) && len(c.Library.QQAppIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_QQ_APP_IDS")
		}
		if c.identityEnabled(enum.IdentityWeibo) && len(c.Library.WeiboAppIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTSVC_WEIBO_APP_IDS")
		}
	}
	c.Mode = defaultEnv("MODE", "production")
	if c.Mode != "production" && c.Mode != "development" {
		return Config{}, invalidConfig("ACCOUNTSVC_MODE")
	}
	c.HTTPAddr = defaultEnv("HTTP_ADDR", "127.0.0.1:8080")
	if !validListenAddress(c.HTTPAddr) {
		return Config{}, invalidConfig("ACCOUNTSVC_HTTP_ADDR")
	}
	if os.Getenv("ACCOUNTSVC_INTERNAL_ADDR") != "" {
		return Config{}, errors.New("accountsvc: remove ACCOUNTSVC_INTERNAL_ADDR and update access policies for the single HTTP_ADDR listener")
	}
	c.ShutdownTimeout, err = durationEnv("SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	if c.ShutdownTimeout <= 15*time.Second {
		return Config{}, invalidConfig("ACCOUNTSVC_SHUTDOWN_TIMEOUT (must exceed 15s)")
	}
	c.TLSCertFile = env("TLS_CERT_FILE")
	c.TLSKeyFile = env("TLS_KEY_FILE")
	c.TLSEnabled = c.Mode == "production" || c.TLSCertFile != "" || c.TLSKeyFile != ""
	if raw := env("TLS_ENABLED"); raw != "" {
		c.TLSEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_ENABLED")
		}
		if !c.TLSEnabled && (c.TLSCertFile != "" || c.TLSKeyFile != "") {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_ENABLED / TLS_CERT_FILE / TLS_KEY_FILE (certificate conflicts with TLS_ENABLED=false)")
		}
	}
	if c.TLSEnabled {
		if c.TLSCertFile == "" {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_CERT_FILE")
		}
		if c.TLSKeyFile == "" {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_KEY_FILE")
		}
		if _, err = os.Stat(c.TLSCertFile); err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_CERT_FILE")
		}
		if _, err = os.Stat(c.TLSKeyFile); err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_KEY_FILE")
		}
		cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
		if err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_TLS_CERT_FILE / ACCOUNTSVC_TLS_KEY_FILE")
		}
		c.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	if raw := env("TRUSTED_PROXY_CIDRS"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err != nil {
				return Config{}, invalidConfig("ACCOUNTSVC_TRUSTED_PROXY_CIDRS")
			}
			c.TrustedProxyCIDRs = append(c.TrustedProxyCIDRs, prefix.Masked())
		}
	}
	if c.identityEnabled(enum.IdentityEmail) {
		port, err := strconv.Atoi(env("SMTP_PORT"))
		if err != nil || port < 1 || port > 65535 {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_PORT")
		}
		c.SMTP = smtp.Config{Host: env("SMTP_HOST"), Port: port, From: env("SMTP_FROM"), Username: env("SMTP_USERNAME"), Password: os.Getenv("ACCOUNTSVC_SMTP_PASSWORD"), TLSMode: defaultEnv("SMTP_TLS_MODE", "implicit")}
		c.SMTP.Timeout, err = durationEnv("SMTP_TIMEOUT", 10*time.Second)
		if err != nil {
			return Config{}, err
		}
		if c.SMTP.Username == "" && (c.Mode == "production" || c.SMTP.Password != "") {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_USERNAME")
		}
		if c.SMTP.Password == "" && (c.Mode == "production" || c.SMTP.Username != "") {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_PASSWORD")
		}
		if c.Mode == "production" && c.SMTP.TLSMode == "none" {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_TLS_MODE")
		}
		// 不裁剪空白：首尾空白须被拒绝而非静默去除。
		c.SMTPBrand = os.Getenv("ACCOUNTSVC_SMTP_BRAND")
		if smtp.ValidateBrand(c.SMTPBrand) != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_BRAND")
		}
		if _, err = smtp.New(c.SMTP); err != nil {
			for _, field := range []struct{ token, name string }{{"Host", "HOST"}, {"From", "FROM"}, {"TLSMode", "TLS_MODE"}, {"Timeout", "TIMEOUT"}} {
				if strings.Contains(err.Error(), field.token) {
					return Config{}, invalidConfig("ACCOUNTSVC_SMTP_" + field.name)
				}
			}
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP configuration")
		}
	}
	if raw := os.Getenv("ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS"); strings.TrimSpace(raw) != "" {
		if c.IntrospectionBasicClients, err = introspection.ParseClients(raw); err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS")
		}
	}
	if raw := os.Getenv("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS"); strings.TrimSpace(raw) != "" {
		if c.PurgeFeedBasicClients, err = basicclients.Parse(raw, "ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS"); err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS")
		}
		// 一份凭据不能同时打开内省与已清理用户拉取两个独立授权的接口。
		if basicclients.SharedSecret(c.IntrospectionBasicClients, c.PurgeFeedBasicClients) {
			return Config{}, invalidConfig("ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS (secret shared with ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS)")
		}
	}
	if err = c.loadAdmin(); err != nil {
		return Config{}, err
	}
	if err = c.loadService(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) loadAdmin() error {
	var err error
	if raw := env("ADMIN_ENABLED"); raw != "" {
		c.AdminEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return invalidConfig("ACCOUNTSVC_ADMIN_ENABLED")
		}
	}
	if !c.AdminEnabled {
		for _, name := range []string{"ADMIN_ISSUER", "ADMIN_AUDIENCE", "ADMIN_ROLES_CLAIM", "ADMIN_USERNAME_CLAIM"} {
			if env(name) != "" {
				return invalidConfig("ACCOUNTSVC_" + name + " (ADMIN_ENABLED is false)")
			}
		}
		return nil
	}
	c.Admin = adminauth.Config{Issuer: env("ADMIN_ISSUER"), Audience: env("ADMIN_AUDIENCE"), RolesClaim: defaultEnv("ADMIN_ROLES_CLAIM", "/roles"), UsernameClaim: defaultEnv("ADMIN_USERNAME_CLAIM", "/preferred_username")}
	if err = c.Admin.Validate(); err != nil {
		return invalidConfig("ACCOUNTSVC_ADMIN_ISSUER / ADMIN_AUDIENCE / ADMIN_ROLES_CLAIM / ADMIN_USERNAME_CLAIM")
	}
	if c.Admin.Audience == c.Library.JWTAudience {
		return invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
	}
	for _, app := range append(append([]accountkit.WeChatApp(nil), c.Library.WeChatApps...), c.Library.WeChatMiniProgramApps...) {
		if c.Admin.Audience == app.AppID {
			return invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
		}
	}
	for _, id := range c.Library.AppleBundleIDs {
		if c.Admin.Audience == id {
			return invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
		}
	}
	return nil
}

// ServiceConfig 是业务服务客户端凭据 Bearer 令牌的签发方、受众与逐接口 azp 允许列表。
type ServiceConfig struct {
	Issuer, Audience           string
	IntrospectionBearerClients []string
	PurgeFeedBearerClients     []string
}

// Enabled 报告是否有任一接口接受 Bearer 令牌。
func (s ServiceConfig) Enabled() bool {
	return len(s.IntrospectionBearerClients) > 0 || len(s.PurgeFeedBearerClients) > 0
}

// loadService 读取服务令牌配置并校验：两列表不相交、不得半配置，issuer 与消费者
// issuer 不同，audience 不得与消费者/管理员 audience、任一第三方应用或客户端 ID、任一 azp 相同
// （否则那个客户端自己的 ID token 可冒充访问令牌）。错误只含变量名。
func (c *Config) loadService() error {
	s := &c.Service
	var err error
	if s.IntrospectionBearerClients, err = parseBearerClients(env("INTROSPECTION_BEARER_CLIENTS"), "ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS"); err != nil {
		return err
	}
	if s.PurgeFeedBearerClients, err = parseBearerClients(env("PURGE_FEED_BEARER_CLIENTS"), "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"); err != nil {
		return err
	}
	// audience 不经 env() 去空白，以便拒绝首尾空白。
	s.Issuer, s.Audience = env("SERVICE_ISSUER"), os.Getenv("ACCOUNTSVC_SERVICE_AUDIENCE")
	if !s.Enabled() {
		if s.Issuer != "" || s.Audience != "" {
			return invalidConfig("ACCOUNTSVC_SERVICE_ISSUER / ACCOUNTSVC_SERVICE_AUDIENCE (no bearer clients configured)")
		}
		return nil
	}
	for _, id := range s.PurgeFeedBearerClients {
		if slices.Contains(s.IntrospectionBearerClients, id) {
			return invalidConfig("ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS (client shared with ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS)")
		}
	}
	if !oidcjwt.ValidIssuer(s.Issuer) || strings.TrimRight(s.Issuer, "/") == strings.TrimRight(c.Library.JWTIssuer, "/") {
		return invalidConfig("ACCOUNTSVC_SERVICE_ISSUER")
	}
	if s.Audience == "" || strings.TrimSpace(s.Audience) != s.Audience || slices.Contains(c.reservedAudiences(), s.Audience) {
		return invalidConfig("ACCOUNTSVC_SERVICE_AUDIENCE")
	}
	return nil
}

// reservedAudiences 是服务 audience 不得使用的值：其他令牌的受众、第三方登录的应用与客户端 ID，
// 以及服务令牌允许的 azp。
func (c *Config) reservedAudiences() []string {
	l := c.Library
	out := []string{l.JWTAudience, l.FacebookAppID}
	if c.AdminEnabled {
		out = append(out, c.Admin.Audience)
	}
	for _, app := range append(append([]accountkit.WeChatApp(nil), l.WeChatApps...), l.WeChatMiniProgramApps...) {
		out = append(out, app.AppID)
	}
	for _, app := range l.AlipayApps {
		out = append(out, app.AppID)
	}
	for _, p := range l.OIDCProviders {
		out = append(out, p.ClientIDs...)
	}
	out = append(out, l.AppleBundleIDs...)
	out = append(out, l.GoogleClientIDs...)
	out = append(out, l.QQAppIDs...)
	out = append(out, l.WeiboAppIDs...)
	out = append(out, c.Service.IntrospectionBearerClients...)
	return append(out, c.Service.PurgeFeedBearerClients...)
}

// parseBearerClients 解析 JSON 字符串数组；未设置或空数组表示不启用。variable 只用于错误信息。
func parseBearerClients(raw, variable string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := invalidConfig(variable)
	d := json.NewDecoder(strings.NewReader(raw))
	var ids []string
	if !strings.HasPrefix(raw, "[") || d.Decode(&ids) != nil {
		return nil, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !serviceauth.ValidClientID(id) || seen[id] {
			return nil, invalid
		}
		seen[id] = true
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, nil
}

// aliyunTemplateKeys 是阿里云短信模板变量后缀与验证码用途的对应；将来的国际短信使用独立变量。
var aliyunTemplateKeys = []struct {
	suffix  string
	purpose enum.CodePurpose
}{
	{"TEMPLATE_SIGN_IN", enum.PurposeSignIn},
	{"TEMPLATE_BIND", enum.PurposeBind},
	{"TEMPLATE_REAUTH", enum.PurposeReauth},
	{"TEMPLATE_SIGN_IN_COMPLETE", enum.PurposeSignInComplete},
}

// loadSMS 解析可选短信服务商；未设置 ACCOUNTSVC_SMS_PROVIDER 时返回 nil，
// 但仍拒绝残留的服务商变量以发现拼写错误。错误只含变量名，不回显值。
func loadSMS(mode string) (*aliyun.Config, error) {
	provider := env("SMS_PROVIDER")
	if provider == "" {
		for _, entry := range os.Environ() {
			if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "ACCOUNTSVC_SMS_ALIYUN_") && env(strings.TrimPrefix(name, "ACCOUNTSVC_")) != "" {
				return nil, invalidConfig("ACCOUNTSVC_SMS_PROVIDER (" + name + " is set)")
			}
		}
		return nil, nil
	}
	if provider != "aliyun" {
		return nil, invalidConfig("ACCOUNTSVC_SMS_PROVIDER")
	}
	known := map[string]bool{"ACCESS_KEY_ID": true, "ACCESS_KEY_SECRET": true, "SIGN_NAME": true, "ENDPOINT": true}
	for _, key := range aliyunTemplateKeys {
		known[key.suffix] = true
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if suffix, ok := strings.CutPrefix(name, "ACCOUNTSVC_SMS_ALIYUN_"); ok && !known[suffix] && env(strings.TrimPrefix(name, "ACCOUNTSVC_")) != "" {
			return nil, invalidConfig(name + " (unknown variable)")
		}
	}
	cfg := &aliyun.Config{
		AccessKeyID:     env("SMS_ALIYUN_ACCESS_KEY_ID"),
		AccessKeySecret: env("SMS_ALIYUN_ACCESS_KEY_SECRET"),
		SignName:        env("SMS_ALIYUN_SIGN_NAME"),
		Endpoint:        env("SMS_ALIYUN_ENDPOINT"),
		Templates:       make(map[enum.CodePurpose]string, len(aliyunTemplateKeys)),
	}
	for name, value := range map[string]string{"ACCESS_KEY_ID": cfg.AccessKeyID, "ACCESS_KEY_SECRET": cfg.AccessKeySecret, "SIGN_NAME": cfg.SignName} {
		if value == "" {
			return nil, invalidConfig("ACCOUNTSVC_SMS_ALIYUN_" + name)
		}
	}
	for _, key := range aliyunTemplateKeys {
		code := env("SMS_ALIYUN_" + key.suffix)
		if code == "" {
			return nil, invalidConfig("ACCOUNTSVC_SMS_ALIYUN_" + key.suffix)
		}
		cfg.Templates[key.purpose] = code
	}
	if cfg.Endpoint != "" {
		if aliyun.ValidateEndpoint(cfg.Endpoint, mode != "production") != nil {
			return nil, invalidConfig("ACCOUNTSVC_SMS_ALIYUN_ENDPOINT")
		}
	}
	if _, err := aliyun.New(*cfg); err != nil {
		return nil, invalidConfig("ACCOUNTSVC_SMS_ALIYUN configuration")
	}
	return cfg, nil
}

// smsSender 返回服务注入的短信发送器：省略 Enabled 时显式禁用（不因配置服务商而自动启用 PHONE）；
// 显式启用 PHONE 时使用已校验的服务商；显式集合不含 PHONE 时不注入。
func smsSender(cfg Config, logger *slog.Logger) (sender.SMSCodeSender, error) {
	if cfg.Library.EnabledIdentityKinds == nil {
		return sender.Disabled{}, nil
	}
	if !cfg.identityEnabled(enum.IdentityPhone) || cfg.SMS == nil {
		return nil, nil
	}
	smsCfg := *cfg.SMS
	smsCfg.Logger = logger
	s, err := aliyun.New(smsCfg)
	if err != nil {
		return nil, errors.New("accountsvc: SMS configuration failed")
	}
	return s, nil
}

// 未配置 Enabled 时保持原 SMTP 装配；显式集合决定需要的发送器。
func (c Config) identityEnabled(kind enum.IdentityKind) bool {
	if c.Library.EnabledIdentityKinds == nil {
		return true
	}
	for _, enabled := range c.Library.EnabledIdentityKinds {
		if enabled == kind {
			return true
		}
	}
	return false
}

func validListenAddress(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.ContainsAny(host, "\r\n\t /\\") {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 0 && p <= 65535
}

var metricsInstancePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var libraryErrorName = regexp.MustCompile(`^accountkit: (ACCOUNTSVC_[A-Z0-9_]+):`)
var libraryErrorField = regexp.MustCompile(`^(?:accountkit|user): ([A-Za-z][A-Za-z0-9]*)`)
var libraryAlipayAppError = regexp.MustCompile(`^accountkit: idp: AlipayApps\[([0-9]+)\]\.(AppID|PrivateKey|AlipayPublicKey)(?::| )`)
var libraryAlipayFieldError = regexp.MustCompile(`^accountkit: idp: (AlipaySubjectMode|AlipayGatewayURL)(?::| )`)
var libraryQQFieldError = regexp.MustCompile(`^accountkit: idp: (QQAppIDs|QQSubjectMode|QQAPIBaseURL)(?:\[| )`)
var libraryWeiboFieldError = regexp.MustCompile(`^accountkit: idp: (WeiboAppIDs|WeiboAPIBaseURL)(?:\[| )`)
var alipayAppIDPattern = regexp.MustCompile(`^[0-9]{16}$`)

// 根 ConfigFromEnv 的解析/验证错误可含原始输入；仅取锚定的配置名称，不转出原错误。
func safeLibraryConfigError(err error) error {
	if matches := libraryErrorName.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(matches[1])
	}
	if matches := libraryAlipayAppError.FindStringSubmatch(err.Error()); len(matches) == 3 {
		apps := strings.Split(os.Getenv(envPrefix+"ALIPAY_APP_IDS"), ",")
		index, parseErr := strconv.Atoi(matches[1])
		if parseErr == nil && index < len(apps) && matches[2] != "AppID" {
			appID := strings.TrimSpace(apps[index])
			if alipayAppIDPattern.MatchString(appID) {
				suffix := "PRIVATE_KEY"
				if matches[2] == "AlipayPublicKey" {
					suffix = "PUBLIC_KEY"
				}
				return invalidConfig("ACCOUNTSVC_ALIPAY_" + appID + "_" + suffix)
			}
		}
		return invalidConfig("ACCOUNTSVC_ALIPAY_APP_IDS")
	}
	if matches := libraryAlipayFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if matches[1] == "AlipaySubjectMode" {
			return invalidConfig("ACCOUNTSVC_ALIPAY_SUBJECT_MODE")
		}
		return invalidConfig("ACCOUNTSVC_ALIPAY_GATEWAY_URL")
	}
	if matches := libraryQQFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(map[string]string{"QQAppIDs": "ACCOUNTSVC_QQ_APP_IDS", "QQSubjectMode": "ACCOUNTSVC_QQ_SUBJECT_MODE", "QQAPIBaseURL": "ACCOUNTSVC_QQ_API_BASE_URL"}[matches[1]])
	}
	if matches := libraryWeiboFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(map[string]string{"WeiboAppIDs": "ACCOUNTSVC_WEIBO_APP_IDS", "WeiboAPIBaseURL": "ACCOUNTSVC_WEIBO_API_BASE_URL"}[matches[1]])
	}
	names := map[string]string{"Schema": "AUTH_SCHEMA", "KeyPrefix": "AUTH_KEY_PREFIX", "JWTKeys": "JWT_KEYS / ACCOUNTSVC_JWT_ACTIVE_KEY", "JWTIssuer": "JWT_ISSUER / ACCOUNTSVC_JWT_AUDIENCE", "SubjectHMACKeys": "SUBJECT_HMAC_KEYS / ACCOUNTSVC_SUBJECT_HMAC_ACTIVE_KEY", "SubjectCipherKeys": "SUBJECT_CIPHER_KEYS / ACCOUNTSVC_SUBJECT_CIPHER_ACTIVE_KEY", "AccessTokenTTL": "ACCESS_TOKEN_TTL", "RefreshTokenTTL": "REFRESH_TOKEN_TTL", "RefreshGrace": "REFRESH_GRACE", "ReauthMaxAge": "REAUTH_MAX_AGE", "CodeTTL": "CODE_TTL / ACCOUNTSVC_CODE_COOLDOWN", "Code": "CODE_MAX_ATTEMPTS / ACCOUNTSVC_CODE_DAILY_LIMIT_PER_TARGET / ACCOUNTSVC_CODE_DAILY_LIMIT_PER_IP", "MaxIdentitiesPerKind": "MAX_IDENTITIES_PER_KIND", "DeletionCoolingPeriod": "DELETION_COOLING_PERIOD / ACCOUNTSVC_AUDIT_RETENTION_DAYS", "PurgedUserRetention": "PURGED_USER_RETENTION", "DefaultRegion": "DEFAULT_REGION", "WeChatApps": "WECHAT_APPS", "WeChatMiniProgramApps": "WECHAT_MINI_PROGRAM_APPS", "WeChatSubjectMode": "WECHAT_SUBJECT_MODE", "AppleBundleIDs": "APPLE_BUNDLE_IDS", "AppleNonceTTL": "APPLE_NONCE_TTL", "GoogleClientIDs": "GOOGLE_CLIENT_IDS", "GoogleNonceTTL": "GOOGLE_NONCE_TTL", "WeChatAPIBaseURL": "WECHAT_API_BASE_URL", "AppleJWKSURL": "APPLE_JWKS_URL", "GoogleJWKSURL": "GOOGLE_JWKS_URL", "MaintenanceInterval": "MAINTENANCE_INTERVAL"}
	names["FacebookAppID"] = "FACEBOOK_APP_ID"
	names["FacebookAppSecret"] = "FACEBOOK_APP_SECRET"
	names["FacebookGraphAPIVersion"] = "FACEBOOK_GRAPH_API_VERSION"
	names["FacebookNonceTTL"] = "FACEBOOK_NONCE_TTL"
	names["FacebookGraphAPIBaseURL"] = "FACEBOOK_GRAPH_API_BASE_URL"
	names["FacebookJWKSURL"] = "FACEBOOK_JWKS_URL"
	names["EnabledIdentityKinds"] = "ENABLED_IDENTITY_KINDS"
	names["OIDCProviders"] = "OIDC_PROVIDERS"
	names["RequiredIdentityKinds"] = "REQUIRED_IDENTITY_KINDS"
	if matches := libraryErrorField.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if name, ok := names[matches[1]]; ok {
			return invalidConfig("ACCOUNTSVC_" + name)
		}
	}
	return invalidConfig(envPrefix + " library configuration")
}
