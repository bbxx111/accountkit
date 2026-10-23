// Package accountsvc 装配可选的独立账号服务；嵌入式宿主不依赖此包。
package accountsvc

import (
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
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
	IntrospectionClients map[string][]string
	AdminEnabled         bool
	Admin                adminauth.Config
	MetricsEnabled       bool
	MetricsInstance      string
	command              string
	tlsConfig            *tls.Config
}

func invalidConfig(name string) error { return errors.New("accountsvc: invalid or missing " + name) }
func env(name string) string          { return strings.TrimSpace(os.Getenv("ACCOUNTSVC_" + name)) }
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

// LoadConfig 先校验配置再允许创建依赖；migrate 不读取服务专用设置。
func LoadConfig(command string) (Config, error) {
	if command != "serve" && command != "migrate" {
		return Config{}, errors.New("accountsvc: command must be serve or migrate")
	}
	c := Config{command: command, ShutdownTimeout: 30 * time.Second}
	var err error
	c.Library, err = accountkit.ConfigFromEnv("ACCOUNTKIT_")
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
	if c.Library.EnabledIdentityKinds != nil {
		if c.identityEnabled(enum.IdentityPhone) {
			return Config{}, errors.New("accountsvc: PHONE requires SMS provider support; inject an SMS sender in an embedded host")
		}
		if c.identityEnabled(enum.IdentityWeChat) && len(c.Library.WeChatApps) == 0 && len(c.Library.WeChatMiniProgramApps) == 0 {
			return Config{}, invalidConfig("ACCOUNTKIT_WECHAT_APPS")
		}
		if c.identityEnabled(enum.IdentityApple) && len(c.Library.AppleBundleIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTKIT_APPLE_BUNDLE_IDS")
		}
		if c.identityEnabled(enum.IdentityGoogle) && len(c.Library.GoogleClientIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTKIT_GOOGLE_CLIENT_IDS")
		}
		if c.identityEnabled(enum.IdentityFacebook) && c.Library.FacebookAppID == "" {
			return Config{}, invalidConfig("ACCOUNTKIT_FACEBOOK_APP_ID")
		}
		if c.identityEnabled(enum.IdentityAlipay) && len(c.Library.AlipayApps) == 0 {
			return Config{}, invalidConfig("ACCOUNTKIT_ALIPAY_APP_IDS")
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
		if _, err = smtp.New(c.SMTP); err != nil {
			for _, field := range []struct{ token, name string }{{"Host", "HOST"}, {"From", "FROM"}, {"TLSMode", "TLS_MODE"}, {"Timeout", "TIMEOUT"}} {
				if strings.Contains(err.Error(), field.token) {
					return Config{}, invalidConfig("ACCOUNTSVC_SMTP_" + field.name)
				}
			}
			return Config{}, invalidConfig("ACCOUNTSVC_SMTP configuration")
		}
	}
	c.IntrospectionClients, err = introspection.ParseClients(os.Getenv("ACCOUNTSVC_INTROSPECTION_CLIENTS"))
	if err != nil {
		return Config{}, invalidConfig("ACCOUNTSVC_INTROSPECTION_CLIENTS")
	}
	if raw := env("ADMIN_ENABLED"); raw != "" {
		c.AdminEnabled, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, invalidConfig("ACCOUNTSVC_ADMIN_ENABLED")
		}
	}
	if !c.AdminEnabled {
		for _, name := range []string{"ADMIN_ISSUER", "ADMIN_AUDIENCE", "ADMIN_ROLES_CLAIM", "ADMIN_USERNAME_CLAIM"} {
			if env(name) != "" {
				return Config{}, invalidConfig("ACCOUNTSVC_" + name + " (ADMIN_ENABLED is false)")
			}
		}
		return c, nil
	}
	c.Admin = adminauth.Config{Issuer: env("ADMIN_ISSUER"), Audience: env("ADMIN_AUDIENCE"), RolesClaim: defaultEnv("ADMIN_ROLES_CLAIM", "/roles"), UsernameClaim: defaultEnv("ADMIN_USERNAME_CLAIM", "/preferred_username")}
	if err = c.Admin.Validate(); err != nil {
		return Config{}, invalidConfig("ACCOUNTSVC_ADMIN_ISSUER / ADMIN_AUDIENCE / ADMIN_ROLES_CLAIM / ADMIN_USERNAME_CLAIM")
	}
	if c.Admin.Audience == c.Library.JWTAudience {
		return Config{}, invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
	}
	for _, app := range append(append([]accountkit.WeChatApp(nil), c.Library.WeChatApps...), c.Library.WeChatMiniProgramApps...) {
		if c.Admin.Audience == app.AppID {
			return Config{}, invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
		}
	}
	for _, id := range c.Library.AppleBundleIDs {
		if c.Admin.Audience == id {
			return Config{}, invalidConfig("ACCOUNTSVC_ADMIN_AUDIENCE")
		}
	}
	return c, nil
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
var libraryErrorName = regexp.MustCompile(`^accountkit: (ACCOUNTKIT_[A-Z0-9_]+):`)
var libraryErrorField = regexp.MustCompile(`^(?:accountkit|user): ([A-Za-z][A-Za-z0-9]*)`)
var libraryAlipayAppError = regexp.MustCompile(`^accountkit: idp: AlipayApps\[([0-9]+)\]\.(AppID|PrivateKey|AlipayPublicKey)(?::| )`)
var libraryAlipayFieldError = regexp.MustCompile(`^accountkit: idp: (AlipaySubjectMode|AlipayGatewayURL)(?::| )`)
var alipayAppIDPattern = regexp.MustCompile(`^[0-9]{16}$`)

// 根 ConfigFromEnv 的解析/验证错误可含原始输入；仅取锚定的配置名称，不转出原错误。
func safeLibraryConfigError(err error) error {
	if matches := libraryErrorName.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(matches[1])
	}
	if matches := libraryAlipayAppError.FindStringSubmatch(err.Error()); len(matches) == 3 {
		apps := strings.Split(os.Getenv("ACCOUNTKIT_ALIPAY_APP_IDS"), ",")
		index, parseErr := strconv.Atoi(matches[1])
		if parseErr == nil && index < len(apps) && matches[2] != "AppID" {
			appID := strings.TrimSpace(apps[index])
			if alipayAppIDPattern.MatchString(appID) {
				suffix := "PRIVATE_KEY"
				if matches[2] == "AlipayPublicKey" {
					suffix = "PUBLIC_KEY"
				}
				return invalidConfig("ACCOUNTKIT_ALIPAY_" + appID + "_" + suffix)
			}
		}
		return invalidConfig("ACCOUNTKIT_ALIPAY_APP_IDS")
	}
	if matches := libraryAlipayFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		if matches[1] == "AlipaySubjectMode" {
			return invalidConfig("ACCOUNTKIT_ALIPAY_SUBJECT_MODE")
		}
		return invalidConfig("ACCOUNTKIT_ALIPAY_GATEWAY_URL")
	}
	names := map[string]string{"Schema": "AUTH_SCHEMA", "KeyPrefix": "AUTH_KEY_PREFIX", "JWTKeys": "JWT_KEYS / ACCOUNTKIT_JWT_ACTIVE_KEY", "JWTIssuer": "JWT_ISSUER / ACCOUNTKIT_JWT_AUDIENCE", "SubjectHMACKeys": "SUBJECT_HMAC_KEYS / ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY", "SubjectCipherKeys": "SUBJECT_CIPHER_KEYS / ACCOUNTKIT_SUBJECT_CIPHER_ACTIVE_KEY", "AccessTokenTTL": "ACCESS_TOKEN_TTL", "RefreshTokenTTL": "REFRESH_TOKEN_TTL", "RefreshGrace": "REFRESH_GRACE", "ReauthMaxAge": "REAUTH_MAX_AGE", "CodeTTL": "CODE_TTL / ACCOUNTKIT_CODE_COOLDOWN", "Code": "CODE_MAX_ATTEMPTS / ACCOUNTKIT_CODE_DAILY_LIMIT_PER_TARGET / ACCOUNTKIT_CODE_DAILY_LIMIT_PER_IP", "MaxIdentitiesPerKind": "MAX_IDENTITIES_PER_KIND", "DeletionCoolingPeriod": "DELETION_COOLING_PERIOD / ACCOUNTKIT_AUDIT_RETENTION_DAYS", "DefaultRegion": "DEFAULT_REGION", "WeChatApps": "WECHAT_APPS", "WeChatMiniProgramApps": "WECHAT_MINI_PROGRAM_APPS", "WeChatSubjectMode": "WECHAT_SUBJECT_MODE", "AppleBundleIDs": "APPLE_BUNDLE_IDS", "AppleNonceTTL": "APPLE_NONCE_TTL", "GoogleClientIDs": "GOOGLE_CLIENT_IDS", "GoogleNonceTTL": "GOOGLE_NONCE_TTL", "WeChatAPIBaseURL": "WECHAT_API_BASE_URL", "AppleJWKSURL": "APPLE_JWKS_URL", "GoogleJWKSURL": "GOOGLE_JWKS_URL", "MaintenanceInterval": "MAINTENANCE_INTERVAL"}
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
			return invalidConfig("ACCOUNTKIT_" + name)
		}
	}
	return invalidConfig("ACCOUNTKIT configuration")
}
