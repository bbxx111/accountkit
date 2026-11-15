package accountsvc

// Stable values belong to this configuration or protocol domain.
const (
	envId                            = "ID"
	envAuthMethods                   = "AUTH_METHODS"
	envPermissions                   = "PERMISSIONS"
	envBasicSecrets                  = "BASIC_SECRETS"
	envBearerSubjects                = "BEARER_SUBJECTS"
	envAccountsvcServiceClients      = envPrefix + "SERVICE_CLIENTS"
	envAccountsvcServiceClient       = envPrefix + "SERVICE_CLIENT_"
	envAccountsvcSmsAliyun           = envPrefix + "SMS_ALIYUN_"
	envAccountsvcAdminAudience       = envPrefix + "ADMIN_AUDIENCE"
	envAccountsvcAdminTokenProfile   = envPrefix + "ADMIN_TOKEN_PROFILE"
	envAccountsvcServiceAudience     = envPrefix + "SERVICE_AUDIENCE"
	envAccountsvcServiceTokenProfile = envPrefix + "SERVICE_TOKEN_PROFILE"
	envAccountsvcDatabaseUrl         = envPrefix + "DATABASE_URL"
	envAccountsvcRedisUrl            = envPrefix + "REDIS_URL"
	envAccountsvcSmtpPassword        = envPrefix + "SMTP_PASSWORD"
	envAccountsvcTlsCertFile         = envPrefix + "TLS_CERT_FILE"
	envAccountsvcTlsKeyFile          = envPrefix + "TLS_KEY_FILE"
	envAccountsvcAlipayAppIds        = envPrefix + "ALIPAY_APP_IDS"
	envAccountsvcQqAppIds            = envPrefix + "QQ_APP_IDS"
	envAccountsvcWeiboAppIds         = envPrefix + "WEIBO_APP_IDS"
)
