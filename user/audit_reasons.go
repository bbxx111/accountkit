package user

import "github.com/bbxx111/accountkit/enum"

// Stable wire values; keep persisted bytes compatible.
const (
	auditReasonChannelNotEnabled        = "CHANNEL_NOT_ENABLED"
	auditReasonTargetNotSupported       = "TARGET_NOT_SUPPORTED"
	auditReasonSendFailed               = "SEND_FAILED"
	auditReasonIdentityKindNotEnabled   = "IDENTITY_KIND_NOT_ENABLED"
	auditReasonCodeInvalid              = "CODE_INVALID"
	auditReasonCodeExpired              = "CODE_EXPIRED"
	auditReasonUnavailable              = "UNAVAILABLE"
	auditReasonInternal                 = "INTERNAL"
	auditReasonIdpCredentialInvalid     = "IDP_CREDENTIAL_INVALID"
	auditReasonIdpNonceReplayed         = "IDP_NONCE_REPLAYED"
	auditReasonIdpAppNotAllowed         = "IDP_APP_NOT_ALLOWED"
	auditReasonUserFrozen               = "USER_FROZEN"
	auditReasonUserPendingDeletion      = "USER_PENDING_DELETION"
	auditReasonUnknownToken             = "UNKNOWN_TOKEN"
	auditReasonTokenReuse               = "TOKEN_REUSE"
	auditReasonGraceUnavailable         = "GRACE_UNAVAILABLE"
	auditReasonGraceReplay              = "GRACE_REPLAY"
	auditReasonSessionRevoked           = "SESSION_REVOKED"
	auditReasonSessionExpired           = "SESSION_EXPIRED"
	auditReasonNotAnchor                = "NOT_ANCHOR"
	auditReasonIdentityAlreadyBound     = "IDENTITY_ALREADY_BOUND"
	auditReasonIdentityKindLimit        = "IDENTITY_KIND_LIMIT"
	auditReasonAlreadyBound             = "ALREADY_BOUND"
	auditReasonLastAnchor               = "LAST_ANCHOR"
	auditReasonInsufficientScope        = "INSUFFICIENT_SCOPE"
	auditReasonReauthenticationRequired = "REAUTHENTICATION_REQUIRED"
	auditReasonIdentityUnchanged        = "IDENTITY_UNCHANGED"
	auditReasonInvalidArgument          = "INVALID_ARGUMENT"
	auditReasonNotFound                 = "NOT_FOUND"
	auditReasonTokenInvalid             = "TOKEN_INVALID"
	auditReasonNewUser                  = "NEW_USER"
	auditReasonSignInCompletionInvalid  = "SIGN_IN_COMPLETION_INVALID"
	auditReasonSignInAlreadyCompleted   = "SIGN_IN_ALREADY_COMPLETED"
	auditReasonRateLimited              = "RATE_LIMITED"
)

// auditUserStateReason preserves even unspecified and unknown-state legacy output.
func auditUserStateReason(state enum.UserState) string {
	switch state {
	case enum.UserActive:
		return "USER_ACTIVE"
	case enum.UserFrozen:
		return auditReasonUserFrozen
	case enum.UserPendingDeletion:
		return auditReasonUserPendingDeletion
	case enum.UserDeleted:
		return "USER_DELETED"
	default:
		return "USER_" + state.String()
	}
}
