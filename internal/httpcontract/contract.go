// Package httpcontract owns shared HTTP protocol names and response policies.
package httpcontract

import (
	"net/http"
	"regexp"
)

const (
	HeaderAuthorization   = "Authorization"
	HeaderWWWAuthenticate = "WWW-Authenticate"
	HeaderContentType     = "Content-Type"
	HeaderRequestID       = "X-Request-Id"
	HeaderDeviceID        = "X-Device-Id"
	HeaderDeviceName      = "X-Device-Name"
	MediaTypeJSON         = "application/json"
	ContentTypeJSON       = "application/json; charset=utf-8"
	MediaTypeForm         = "application/x-www-form-urlencoded"
	TokenTypeBearer       = "Bearer"
	GrantTypeRefreshToken = "refresh_token"
	MessageInternal       = "internal error"
)

// SetNoStore disables storage without adding the OAuth-specific Pragma policy.
func SetNoStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

// SetOAuthNoStore applies the existing OAuth credential response cache policy.
func SetOAuthNoStore(w http.ResponseWriter) { SetNoStore(w); w.Header().Set("Pragma", "no-cache") }

// BearerChallenge constructs the supported realm and error combinations verbatim.
func BearerChallenge(realm, errorCode string) string {
	s := TokenTypeBearer
	if realm != "" {
		s += ` realm="` + realm + `"`
	}
	if errorCode != "" {
		if realm != "" {
			s += ","
		}
		s += ` error="` + errorCode + `"`
	}
	return s
}

// BasicChallenge constructs a Basic realm challenge.
func BasicChallenge(realm string) string { return `Basic realm="` + realm + `"` }

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidRequestID checks the shared request identifier syntax without normalization.
func ValidRequestID(value string) bool { return requestIDPattern.MatchString(value) }

// OAuth error codes and shared authentication reasons.
const (
	OAuthErrorInvalidRequest         = "invalid_request"
	OAuthErrorUnsupportedGrantType   = "unsupported_grant_type"
	OAuthErrorInvalidGrant           = "invalid_grant"
	OAuthErrorTemporarilyUnavailable = "temporarily_unavailable"
	OAuthErrorServerError            = "server_error"
	OAuthErrorInvalidClient          = "invalid_client"
	OAuthErrorInvalidToken           = "invalid_token"
	OAuthErrorInsufficientScope      = "insufficient_scope"
	ReasonTokenMissing               = "TOKEN_MISSING"
	ReasonTokenInvalid               = "TOKEN_INVALID"
)

// Reasons shared across HTTP surfaces; these are not status or audit identifiers.
const (
	ReasonInvalidPageToken         = "INVALID_PAGE_TOKEN"
	ReasonInvalidID                = "INVALID_ID"
	ReasonNotFound                 = "NOT_FOUND"
	ReasonDeletionBlocked          = "DELETION_BLOCKED"
	ReasonInvalidAccountState      = "INVALID_ACCOUNT_STATE"
	ReasonUserFrozen               = "USER_FROZEN"
	ReasonInvalidArgument          = "INVALID_ARGUMENT"
	ReasonDependencyUnavailable    = "DEPENDENCY_UNAVAILABLE"
	ReasonInsufficientScope        = "INSUFFICIENT_SCOPE"
	ReasonReauthenticationRequired = "REAUTHENTICATION_REQUIRED"
	ReasonMalformedBody            = "MALFORMED_BODY"
	ReasonInvalidClient            = "INVALID_CLIENT"
	ReasonClientNotAuthorized      = "CLIENT_NOT_AUTHORIZED"
)
