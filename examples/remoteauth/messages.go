package remoteauth

// Stable values belong to this configuration or protocol domain.
const (
	messageInvalidToken              = "invalid or expired access token"
	messageInsufficientScope         = "token scope does not permit this operation"
	messageReauthenticationRequired  = "recent authentication required for this operation"
	messageAuthenticationUnavailable = "authentication temporarily unavailable"
	messageBearerRequired            = "bearer token required"
)
