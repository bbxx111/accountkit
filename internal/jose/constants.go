// Package jose owns JOSE protocol names, independently of token issuing and identity systems.
package jose

const (
	HeaderType           = "typ"
	HeaderKeyID          = "kid"
	HeaderJWKURL         = "jku"
	HeaderX509URL        = "x5u"
	ClaimIssuer          = "iss"
	ClaimAudience        = "aud"
	ClaimAuthorizedParty = "azp"
	ClaimSubject         = "sub"
	ClaimClientID        = "client_id"
	ClaimJWTID           = "jti"
	ClaimExpiration      = "exp"
	ClaimIssuedAt        = "iat"
	ClaimScope           = "scope"
	ClaimNonce           = "nonce"
	AccessTokenType      = "at+jwt"
	AccessTokenMediaType = "application/at+jwt"
	JWKTypeRSA           = "RSA"
	JWKTypeEC            = "EC"
	JWKCurveP256         = "P-256"
	JWKUseSignature      = "sig"
	JWKOperationVerify   = "verify"
)
