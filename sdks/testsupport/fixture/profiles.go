package main

// Stable protocol values retain their existing wire bytes.
const (
	profileAccountsvc     = "accountsvc"
	profileExtended       = "extended"
	profileCustom         = "custom"
	profileCustomExtended = "custom-extended"
	defaultAPIPrefix      = "/v1"
	defaultAdminPrefix    = "/admin/v1"
	customAPIPrefix       = "/custom/account"
	customAdminPrefix     = "/custom/admin"
	controlAdminTokenPath = "/__fixture/admin-token"
	controlCodePath       = "/__fixture/code"
	controlCredentialPath = "/__fixture/credential"
	controlReadyPath      = "/__fixture/ready"
	serviceBaseline       = "v1.0.0"
)
