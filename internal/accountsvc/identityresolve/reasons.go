package identityresolve

// HTTP reasons remain distinct from google.rpc statuses and audit reasons.
const (
	reasonIdentityResolutionNotConfigured = "IDENTITY_RESOLUTION_NOT_CONFIGURED"
	reasonInternal                        = "INTERNAL"
)
