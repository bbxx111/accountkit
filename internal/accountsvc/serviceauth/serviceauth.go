// Package serviceauth authenticates business-service callers of accountsvc's internal
// interfaces with HTTP Basic credentials or client-credentials Bearer access tokens. It
// only decides; each handler maps the Result to its own error format.
package serviceauth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/httpcredentials"
)

// Outcome classifies an authentication attempt.
type Outcome int

const (
	// OK means the caller is authenticated and authorized for this interface.
	OK Outcome = iota
	// Unauthenticated means credentials are missing, malformed or rejected (401).
	Unauthenticated
	// Forbidden means an authenticated client is not allowed on this interface (403).
	Forbidden
	// Unavailable means the token issuer could not be reached (503).
	Unavailable
)

// Authentication methods reported in Result.Method.
const (
	MethodNone   = "none"
	MethodBasic  = "basic"
	MethodBearer = "bearer"
)

const (
	defaultTimeout   = 3 * time.Second
	maxTokenLifetime = time.Hour
	maxClientID      = 256
)

// Result is the decision for one request.
type Result struct {
	Outcome Outcome
	// ClientID is the Basic user name or the token's configured client claim; set only for OK and Forbidden.
	ClientID string
	Method   string
	// BearerAttempted is true when the request carried exactly one Bearer token and Bearer is enabled.
	BearerAttempted bool
}

// Config describes one interface's accepted callers.
type Config struct {
	// Realm names the protection space in challenges, e.g. "accountsvc-introspection".
	Realm string
	// Endpoint labels log records, e.g. "introspect".
	Endpoint string
	// Registry holds the shared policy; nil means an empty registry.
	Registry *Registry
	// RequiredPermission names the capability protected by this authenticator.
	RequiredPermission string
	// BearerVerifier is required exactly when the registry enables Bearer.
	BearerVerifier *oidcjwt.Verifier
	// Timeout bounds waiting for signing keys; zero means three seconds.
	Timeout time.Duration
	Logger  *slog.Logger
}

// Authenticator applies one interface's Config.
type Authenticator struct {
	realm, endpoint string
	basic           *basicclients.Matcher
	tokens          *oidcjwt.Verifier
	timeout         time.Duration
	logger          *slog.Logger
	registry        *Registry
	permission      string
}

// ValidClientID reports whether id is usable as an allowed client identifier.
func ValidClientID(id string) bool {
	return id != "" && len(id) <= maxClientID && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// New validates cfg. An Authenticator with neither method enabled rejects every request.
func New(cfg Config) (*Authenticator, error) {
	if cfg.Realm == "" || strings.ContainsAny(cfg.Realm, `"\`) {
		return nil, errors.New("serviceauth: realm is required")
	}
	if cfg.Registry == nil {
		cfg.Registry, _ = NewRegistry(nil)
	}
	if !validPermission(cfg.RequiredPermission) {
		return nil, errors.New("serviceauth: a registered required permission is required")
	}
	if cfg.Registry.BearerEnabled() != (cfg.BearerVerifier != nil) {
		return nil, errors.New("serviceauth: bearer registry and a token verifier must be configured together")
	}
	a := &Authenticator{realm: cfg.Realm, endpoint: cfg.Endpoint, tokens: cfg.BearerVerifier, timeout: cfg.Timeout, logger: cfg.Logger, registry: cfg.Registry, permission: cfg.RequiredPermission}
	a.basic = cfg.Registry.basic
	if a.timeout <= 0 {
		a.timeout = defaultTimeout
	}
	if a.logger == nil {
		a.logger = slog.New(slog.DiscardHandler)
	}
	return a, nil
}

// Enabled reports whether this capability has an authorized client.
func (a *Authenticator) Enabled() bool { return a.registry.HasPermission(a.permission) }

// Authenticate decides one request. It reads credentials only when exactly one
// Authorization header is present and never logs them.
func (a *Authenticator) Authenticate(r *http.Request) Result {
	unauthenticated := Result{Outcome: Unauthenticated, Method: MethodNone}
	if !a.Enabled() {
		return unauthenticated
	}
	scheme, credentials, ok := httpcredentials.Parse(r)
	switch {
	case scheme == "basic" && a.basic != nil:
		if !ok {
			return Result{Outcome: Unauthenticated, Method: MethodBasic}
		}
		// Normalize only the parsed header on a private request copy. Match retains
		// responsibility for decoding Basic credentials and checking secrets.
		normalized := r.Clone(r.Context())
		normalized.Header.Set("Authorization", "Basic "+credentials)
		if id, ok := a.basic.Match(normalized); ok {
			return a.authorize(id, MethodBasic)
		}
		return Result{Outcome: Unauthenticated, Method: MethodBasic}
	case scheme == "bearer" && a.tokens != nil:
		if !ok {
			return Result{Outcome: Unauthenticated, Method: MethodBearer, BearerAttempted: true}
		}
		return a.bearer(r.Context(), credentials)
	}
	return unauthenticated
}

func (a *Authenticator) bearer(parent context.Context, raw string) Result {
	invalid := Result{Outcome: Unauthenticated, Method: MethodBearer, BearerAttempted: true}
	if raw == "" || strings.ContainsAny(raw, " \t") {
		return invalid
	}
	ctx, cancel := context.WithTimeout(parent, a.timeout)
	defer cancel()
	claims, err := a.tokens.Parse(ctx, raw)
	if errors.Is(err, oidcjwt.ErrUnavailable) {
		return Result{Outcome: Unavailable, Method: MethodBearer}
	}
	if err != nil {
		return invalid
	}
	exp, expErr := claims.GetExpirationTime()
	iat, iatErr := claims.GetIssuedAt()
	if expErr != nil || iatErr != nil || exp == nil || iat == nil || exp.Sub(iat.Time) > maxTokenLifetime {
		return invalid
	}
	clientID, ok := claims[a.tokens.ClientIDClaim()].(string)
	if !ok || !ValidClientID(clientID) {
		return invalid
	}
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return invalid
	}
	client, exists := a.registry.clients[clientID]
	if !exists {
		return a.authorize(clientID, MethodBearer)
	}
	if !client.methods[MethodBearer] {
		return Result{Outcome: Unauthenticated, Method: MethodBearer}
	}
	if !client.subjects[sub] {
		return invalid
	}
	scope, present := claims["scope"]
	scopes, valid := tokenScopes(scope, present)
	if !valid {
		return invalid
	}
	if !scopes[a.permission] {
		return a.forbidden(clientID, MethodBearer)
	}
	return a.authorize(clientID, MethodBearer)
}

func tokenScopes(value any, present bool) (map[string]bool, bool) {
	scopes := map[string]bool{}
	if !present {
		return scopes, true
	}
	scope, ok := value.(string)
	if !ok {
		return nil, false
	}
	if scope == "" {
		return scopes, true
	}
	for _, item := range strings.Split(scope, " ") {
		if item == "" {
			return nil, false
		}
		for i := 0; i < len(item); i++ {
			c := item[i]
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				return nil, false
			}
		}
		scopes[item] = true
	}
	return scopes, true
}

func (a *Authenticator) authorize(id, method string) Result {
	client, exists := a.registry.clients[id]
	if exists && !client.methods[method] {
		return Result{Outcome: Unauthenticated, Method: method}
	}
	if !exists || !client.permissions[a.permission] {
		return a.forbidden(id, method)
	}
	return Result{Outcome: OK, ClientID: id, Method: method}
}

func (a *Authenticator) forbidden(id, method string) Result {
	a.logger.Info("accountsvc: service client not authorized for interface", "client_id", id, "endpoint", a.endpoint, "auth_method", method)
	return Result{Outcome: Forbidden, ClientID: id, Method: method}
}

// Challenges returns the WWW-Authenticate values for res: one per enabled method on 401,
// with error="invalid_token" only after an invalid Bearer attempt. Forbidden Bearer
// requests receive insufficient_scope; unified Basic permission refusals receive none.
func (a *Authenticator) Challenges(res Result) []string {
	switch res.Outcome {
	case Unauthenticated:
		var out []string
		if a.basic != nil {
			out = append(out, `Basic realm="`+a.realm+`"`)
		}
		if a.tokens != nil {
			bearer := `Bearer realm="` + a.realm + `"`
			if res.BearerAttempted {
				bearer += `, error="invalid_token"`
			}
			out = append(out, bearer)
		}
		return out
	case Forbidden:
		if res.Method == MethodBasic {
			return nil
		}
		return []string{`Bearer realm="` + a.realm + `", error="insufficient_scope"`}
	}
	return nil
}

// WriteChallenges adds each challenge as its own WWW-Authenticate header line.
func WriteChallenges(w http.ResponseWriter, challenges []string) {
	for _, c := range challenges {
		w.Header().Add("WWW-Authenticate", c)
	}
}
