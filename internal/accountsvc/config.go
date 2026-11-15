// Package accountsvc 装配可选的独立账号服务；嵌入式宿主不依赖此包。
package accountsvc

import (
	"crypto/tls"
	"errors"
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
	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
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
	Library              accountkit.Config
	Mode                 string
	DatabaseURL          string
	RedisURL             string
	HTTPAddr             string
	TLSEnabled           bool // LoadConfig 解析后的有效 HTTP TLS 模式。
	TLSCertFile          string
	TLSKeyFile           string
	StartupTimeout       time.Duration
	ShutdownTimeout      time.Duration
	TrustedProxyCIDRs    []netip.Prefix
	SMTP                 smtp.Config
	SMS                  *aliyun.Config           // nil 表示未配置短信服务商
	EmailTemplates       *emailtemplates.Renderer // nil 表示使用嵌入的无品牌默认清单
	EmailTemplatesDigest string                   // 清单 SHA-256 的前 12 位十六进制，仅用于启动日志核对
	Service              ServiceConfig
	ServiceClients       *serviceauth.Registry // serve 始终加载不可变注册表，包括空注册表。
	AdminEnabled         bool
	Admin                adminauth.Config
	MetricsEnabled       bool
	MetricsInstance      string
	command              string
	tlsConfig            *tls.Config
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
		return 0, invalidConfig(envPrefix + name)
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
	if command != commandServe && command != commandMigrate {
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
		return Config{}, invalidConfig(envAccountsvcDatabaseUrl)
	}
	if _, err = pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return Config{}, invalidConfig(envAccountsvcDatabaseUrl)
	}
	c.RedisURL = env("REDIS_URL")
	u, err = url.Parse(c.RedisURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Fragment != "" {
		return Config{}, invalidConfig(envAccountsvcRedisUrl)
	}
	if _, err = redis.ParseURL(c.RedisURL); err != nil {
		return Config{}, invalidConfig(envAccountsvcRedisUrl)
	}
	c.StartupTimeout, err = durationEnv("STARTUP_TIMEOUT", time.Minute)
	if err != nil {
		return Config{}, err
	}
	if command == commandMigrate {
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
	if c.SMS, err = loadSMS(defaultEnv("MODE", modeProduction)); err != nil {
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
			return Config{}, invalidConfig(envAccountsvcAlipayAppIds)
		}
		if c.identityEnabled(enum.IdentityQQ) && len(c.Library.QQAppIDs) == 0 {
			return Config{}, invalidConfig(envAccountsvcQqAppIds)
		}
		if c.identityEnabled(enum.IdentityWeibo) && len(c.Library.WeiboAppIDs) == 0 {
			return Config{}, invalidConfig(envAccountsvcWeiboAppIds)
		}
	}
	c.Mode = defaultEnv("MODE", modeProduction)
	if c.Mode != modeProduction && c.Mode != modeDevelopment {
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
	c.TLSEnabled = c.Mode == modeProduction || c.TLSCertFile != "" || c.TLSKeyFile != ""
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
			return Config{}, invalidConfig(envAccountsvcTlsCertFile)
		}
		if c.TLSKeyFile == "" {
			return Config{}, invalidConfig(envAccountsvcTlsKeyFile)
		}
		if _, err = os.Stat(c.TLSCertFile); err != nil {
			return Config{}, invalidConfig(envAccountsvcTlsCertFile)
		}
		if _, err = os.Stat(c.TLSKeyFile); err != nil {
			return Config{}, invalidConfig(envAccountsvcTlsKeyFile)
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
		c.SMTP = smtp.Config{Host: env("SMTP_HOST"), Port: port, From: env("SMTP_FROM"), Username: env("SMTP_USERNAME"), Password: os.Getenv(envAccountsvcSmtpPassword), TLSMode: defaultEnv("SMTP_TLS_MODE", smtp.TLSModeImplicit)}
		c.SMTP.Timeout, err = durationEnv("SMTP_TIMEOUT", 10*time.Second)
		if err != nil {
			return Config{}, err
		}
		if c.SMTP.Username == "" && (c.Mode == modeProduction || c.SMTP.Password != "") {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_USERNAME")
		}
		if c.SMTP.Password == "" && (c.Mode == modeProduction || c.SMTP.Username != "") {
			return Config{}, invalidConfig(envAccountsvcSmtpPassword)
		}
		if c.Mode == modeProduction && c.SMTP.TLSMode == smtp.TLSModeNone {
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP_TLS_MODE")
		}
		if err = c.loadEmailTemplates(env("SMTP_TEMPLATES_FILE")); err != nil {
			return Config{}, err
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
	if err = c.loadServiceClients(); err != nil {
		return Config{}, err
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
		if err := c.loadAdminRoles(); err != nil {
			return err
		}
		if os.Getenv(envAccountsvcAdminTokenProfile) != "" {
			return invalidConfig("ACCOUNTSVC_ADMIN_TOKEN_PROFILE (ADMIN_ENABLED is false)")
		}
		for _, name := range []string{configEnvAdminIssuer, configEnvAdminAudience, configEnvAdminRolesClaim, configEnvAdminUsernameClaim} {
			if env(name) != "" {
				return invalidConfig(envPrefix + name + " (ADMIN_ENABLED is false)")
			}
		}
		return nil
	}
	// 开发模式允许本地明文 IdP；production 保持只接受 HTTPS，错误信息不变。
	if !oidcjwt.ValidProfile(os.Getenv(envAccountsvcAdminTokenProfile)) {
		return invalidConfig(envAccountsvcAdminTokenProfile)
	}
	c.Admin = adminauth.Config{TokenProfile: defaultEnv("ADMIN_TOKEN_PROFILE", oidcjwt.ProfileDefault), Issuer: env(configEnvAdminIssuer), Audience: env(configEnvAdminAudience), RolesClaim: defaultEnv(configEnvAdminRolesClaim, adminauth.DefaultRolesClaim), UsernameClaim: defaultEnv(configEnvAdminUsernameClaim, adminauth.DefaultUsernameClaim), AllowHTTP: c.Mode == modeDevelopment}
	if err = c.Admin.Validate(); err != nil {
		return invalidConfig("ACCOUNTSVC_ADMIN_ISSUER / ADMIN_AUDIENCE / ADMIN_ROLES_CLAIM / ADMIN_USERNAME_CLAIM")
	}
	if c.Admin.Audience == c.Library.JWTAudience {
		return invalidConfig(envAccountsvcAdminAudience)
	}
	if err = c.loadAdminRoles(); err != nil {
		return err
	}
	for _, app := range append(append([]accountkit.WeChatApp(nil), c.Library.WeChatApps...), c.Library.WeChatMiniProgramApps...) {
		if c.Admin.Audience == app.AppID {
			return invalidConfig(envAccountsvcAdminAudience)
		}
	}
	for _, id := range c.Library.AppleBundleIDs {
		if c.Admin.Audience == id {
			return invalidConfig(envAccountsvcAdminAudience)
		}
	}
	return nil
}

// ServiceConfig 是业务服务 Bearer 令牌的共享信任配置。
// AllowHTTP 仅在 development 模式为 true。
type ServiceConfig struct {
	TokenProfile     string
	Issuer, Audience string
	AllowHTTP        bool
	bearerClientIDs  []string
}

// Enabled 报告注册表是否包含 Bearer 客户端。
func (s ServiceConfig) Enabled() bool { return len(s.bearerClientIDs) > 0 }

// loadService 校验 Bearer 信任参数及 issuer/audience 的用途隔离。
func (c *Config) loadService() error {
	s := &c.Service
	// audience 不经 env() 去空白，以便拒绝首尾空白。
	s.Issuer, s.Audience = env("SERVICE_ISSUER"), os.Getenv(envAccountsvcServiceAudience)
	s.TokenProfile = os.Getenv(envAccountsvcServiceTokenProfile)
	s.AllowHTTP = c.Mode == modeDevelopment
	if !s.Enabled() {
		if s.TokenProfile != "" {
			return invalidConfig("ACCOUNTSVC_SERVICE_TOKEN_PROFILE (no bearer clients configured)")
		}
		if s.Issuer != "" || s.Audience != "" {
			return invalidConfig("ACCOUNTSVC_SERVICE_ISSUER / ACCOUNTSVC_SERVICE_AUDIENCE (no bearer clients configured)")
		}
		return nil
	}
	if !oidcjwt.ValidProfile(s.TokenProfile) {
		return invalidConfig(envAccountsvcServiceTokenProfile)
	}
	if s.TokenProfile == "" {
		s.TokenProfile = oidcjwt.ProfileDefault
	}
	validIssuer := oidcjwt.ValidIssuer
	if s.AllowHTTP {
		validIssuer = oidcjwt.ValidPlainIssuer
	}
	if !validIssuer(s.Issuer) || strings.TrimRight(s.Issuer, "/") == strings.TrimRight(c.Library.JWTIssuer, "/") {
		return invalidConfig("ACCOUNTSVC_SERVICE_ISSUER")
	}
	if s.Audience == "" || strings.TrimSpace(s.Audience) != s.Audience || slices.Contains(c.reservedAudiences(), s.Audience) {
		return invalidConfig(envAccountsvcServiceAudience)
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
	return append(out, c.Service.bearerClientIDs...)
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
			if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, envAccountsvcSmsAliyun) && env(strings.TrimPrefix(name, envPrefix)) != "" {
				return nil, invalidConfig("ACCOUNTSVC_SMS_PROVIDER (" + name + " is set)")
			}
		}
		return nil, nil
	}
	if provider != "aliyun" {
		return nil, invalidConfig("ACCOUNTSVC_SMS_PROVIDER")
	}
	known := map[string]bool{configEnvAccessKeyId: true, configEnvAccessKeySecret: true, configEnvSignName: true, "ENDPOINT": true}
	for _, key := range aliyunTemplateKeys {
		known[key.suffix] = true
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if suffix, ok := strings.CutPrefix(name, envAccountsvcSmsAliyun); ok && !known[suffix] && env(strings.TrimPrefix(name, envPrefix)) != "" {
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
	for name, value := range map[string]string{configEnvAccessKeyId: cfg.AccessKeyID, configEnvAccessKeySecret: cfg.AccessKeySecret, configEnvSignName: cfg.SignName} {
		if value == "" {
			return nil, invalidConfig(envAccountsvcSmsAliyun + name)
		}
	}
	for _, key := range aliyunTemplateKeys {
		code := env("SMS_ALIYUN_" + key.suffix)
		if code == "" {
			return nil, invalidConfig(envAccountsvcSmsAliyun + key.suffix)
		}
		cfg.Templates[key.purpose] = code
	}
	if cfg.Endpoint != "" {
		if aliyun.ValidateEndpoint(cfg.Endpoint, mode != modeProduction) != nil {
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
		apps := strings.Split(os.Getenv(envPrefix+configEnvAlipayAppIds), ",")
		index, parseErr := strconv.Atoi(matches[1])
		if parseErr == nil && index < len(apps) && matches[2] != "AppID" {
			appID := strings.TrimSpace(apps[index])
			if alipayAppIDPattern.MatchString(appID) {
				suffix := configEnvPrivateKey
				if matches[2] == "AlipayPublicKey" {
					suffix = configEnvPublicKey
				}
				return invalidConfig("ACCOUNTSVC_ALIPAY_" + appID + "_" + suffix)
			}
		}
		return invalidConfig(envAccountsvcAlipayAppIds)
	}
	if matches := libraryAlipayFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if matches[1] == "AlipaySubjectMode" {
			return invalidConfig("ACCOUNTSVC_ALIPAY_SUBJECT_MODE")
		}
		return invalidConfig("ACCOUNTSVC_ALIPAY_GATEWAY_URL")
	}
	if matches := libraryQQFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(map[string]string{"QQAppIDs": envAccountsvcQqAppIds, "QQSubjectMode": "ACCOUNTSVC_QQ_SUBJECT_MODE", "QQAPIBaseURL": "ACCOUNTSVC_QQ_API_BASE_URL"}[matches[1]])
	}
	if matches := libraryWeiboFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(map[string]string{"WeiboAppIDs": envAccountsvcWeiboAppIds, "WeiboAPIBaseURL": "ACCOUNTSVC_WEIBO_API_BASE_URL"}[matches[1]])
	}
	names := map[string]string{"Schema": "AUTH_SCHEMA", "KeyPrefix": "AUTH_KEY_PREFIX", "JWTKeys": "JWT_KEYS / ACCOUNTSVC_JWT_ACTIVE_KEY", "JWTIssuer": "JWT_ISSUER / ACCOUNTSVC_JWT_AUDIENCE", "SubjectHMACKeys": "SUBJECT_HMAC_KEYS / ACCOUNTSVC_SUBJECT_HMAC_ACTIVE_KEY", "SubjectCipherKeys": "SUBJECT_CIPHER_KEYS / ACCOUNTSVC_SUBJECT_CIPHER_ACTIVE_KEY", "AccessTokenTTL": "ACCESS_TOKEN_TTL", "RefreshTokenTTL": "REFRESH_TOKEN_TTL", "RefreshGrace": "REFRESH_GRACE", "ReauthMaxAge": "REAUTH_MAX_AGE", "CodeTTL": "CODE_TTL / ACCOUNTSVC_CODE_COOLDOWN", "Code": "CODE_MAX_ATTEMPTS / ACCOUNTSVC_CODE_DAILY_LIMIT_PER_TARGET / ACCOUNTSVC_CODE_DAILY_LIMIT_PER_IP", "MaxIdentitiesPerKind": "MAX_IDENTITIES_PER_KIND", "DeletionCoolingPeriod": "DELETION_COOLING_PERIOD / ACCOUNTSVC_AUDIT_RETENTION_DAYS", "PurgedUserRetention": "PURGED_USER_RETENTION", "DefaultRegion": "DEFAULT_REGION", "WeChatApps": "WECHAT_APPS", "WeChatMiniProgramApps": "WECHAT_MINI_PROGRAM_APPS", "WeChatSubjectMode": configEnvWechatSubjectMode, "AppleBundleIDs": "APPLE_BUNDLE_IDS", "AppleNonceTTL": "APPLE_NONCE_TTL", "GoogleClientIDs": "GOOGLE_CLIENT_IDS", "GoogleNonceTTL": "GOOGLE_NONCE_TTL", "WeChatAPIBaseURL": "WECHAT_API_BASE_URL", "AppleJWKSURL": "APPLE_JWKS_URL", "GoogleJWKSURL": "GOOGLE_JWKS_URL", "MaintenanceInterval": "MAINTENANCE_INTERVAL"}
	names["FacebookAppID"] = "FACEBOOK_APP_ID"
	names["FacebookAppSecret"] = "FACEBOOK_APP_SECRET"
	names["FacebookGraphAPIVersion"] = "FACEBOOK_GRAPH_API_VERSION"
	names["FacebookNonceTTL"] = "FACEBOOK_NONCE_TTL"
	names["FacebookGraphAPIBaseURL"] = "FACEBOOK_GRAPH_API_BASE_URL"
	names["FacebookJWKSURL"] = "FACEBOOK_JWKS_URL"
	names["EnabledIdentityKinds"] = "ENABLED_IDENTITY_KINDS"
	names["OIDCProviders"] = configEnvOidcProviders
	names["RequiredIdentityKinds"] = "REQUIRED_IDENTITY_KINDS"
	if matches := libraryErrorField.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if name, ok := names[matches[1]]; ok {
			return invalidConfig(envPrefix + name)
		}
	}
	return invalidConfig(envPrefix + " library configuration")
}
