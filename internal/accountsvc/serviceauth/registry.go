package serviceauth

import (
	"crypto/sha256"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
)

// ClientConfig declares one service identity and its shared authorization policy.
type ClientConfig struct {
	ID             string
	AuthMethods    []string
	Permissions    []string
	BasicSecrets   []string
	BearerSubjects []string
}

// Service permissions belong exclusively to the service-call surface.
const (
	PermissionTokensIntrospect  = "tokens.introspect"
	PermissionPurgedUsersRead   = "purged_users.read"
	PermissionIdentitiesResolve = "identities.resolve"
)

type registeredClient struct {
	methods     map[string]bool
	permissions map[string]bool
	subjects    map[string]bool
}

// Registry is an immutable snapshot of service identities. It retains only hashed secrets.
type Registry struct {
	clients     map[string]registeredClient
	permissions map[string]bool
	basic       *basicclients.Matcher
	bearer      bool
}

// ValidPermission reports exact membership; service permissions do not accept wildcards.
func ValidPermission(permission string) bool {
	return permission == PermissionTokensIntrospect || permission == PermissionPurgedUsersRead || permission == PermissionIdentitiesResolve
}

// NewRegistry validates and snapshots clients. An empty list disables all capabilities.
// Errors describe invalid fields without disclosing client IDs or secrets.
func NewRegistry(clients []ClientConfig) (*Registry, error) {
	r := &Registry{clients: make(map[string]registeredClient, len(clients)), permissions: map[string]bool{}}
	basics := map[string][]string{}
	secrets := map[[sha256.Size]byte]bool{}
	for _, cfg := range clients {
		if !ValidClientID(cfg.ID) || strings.ContainsRune(cfg.ID, ':') {
			return nil, errors.New("serviceauth: invalid client ID")
		}
		if _, exists := r.clients[cfg.ID]; exists {
			return nil, errors.New("serviceauth: duplicate client ID")
		}
		c := registeredClient{methods: map[string]bool{}, permissions: map[string]bool{}, subjects: map[string]bool{}}
		if len(cfg.AuthMethods) == 0 {
			return nil, errors.New("serviceauth: authentication methods are required")
		}
		for _, method := range cfg.AuthMethods {
			if (method != MethodBasic && method != MethodBearer) || c.methods[method] {
				return nil, errors.New("serviceauth: invalid or duplicate authentication method")
			}
			c.methods[method] = true
		}
		if len(cfg.Permissions) == 0 {
			return nil, errors.New("serviceauth: permissions are required")
		}
		for _, permission := range cfg.Permissions {
			if !ValidPermission(permission) || c.permissions[permission] {
				return nil, errors.New("serviceauth: invalid or duplicate permission")
			}
			c.permissions[permission] = true
			r.permissions[permission] = true
		}
		if c.methods[MethodBasic] {
			basic := map[string][]string{cfg.ID: cfg.BasicSecrets}
			if !basicclients.Valid(basic) {
				return nil, errors.New("serviceauth: invalid Basic secrets")
			}
			for _, secret := range cfg.BasicSecrets {
				digest := sha256.Sum256([]byte(secret))
				if secrets[digest] {
					return nil, errors.New("serviceauth: duplicate Basic secret")
				}
				secrets[digest] = true
			}
			basics[cfg.ID] = cfg.BasicSecrets
		} else if len(cfg.BasicSecrets) > 0 {
			return nil, errors.New("serviceauth: Basic secrets require the Basic method")
		}
		if c.methods[MethodBearer] {
			if len(cfg.BearerSubjects) == 0 {
				return nil, errors.New("serviceauth: Bearer subjects are required")
			}
			for _, subject := range cfg.BearerSubjects {
				if !ValidBearerSubject(subject) || c.subjects[subject] {
					return nil, errors.New("serviceauth: invalid or duplicate Bearer subject")
				}
				c.subjects[subject] = true
			}
		} else if len(cfg.BearerSubjects) > 0 {
			return nil, errors.New("serviceauth: Bearer subjects require the Bearer method")
		}
		r.bearer = r.bearer || c.methods[MethodBearer]
		r.clients[cfg.ID] = c
	}
	if len(basics) > 0 {
		var err error
		r.basic, err = basicclients.NewMatcher(basics)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

// ValidBearerSubject validates an exact provider subject without normalizing it.
func ValidBearerSubject(subject string) bool {
	return len(subject) <= 256 && utf8.ValidString(subject) && strings.TrimSpace(subject) != "" && strings.IndexFunc(subject, unicode.IsControl) < 0
}

// HasPermission reports whether any registered client has the capability.
func (r *Registry) HasPermission(permission string) bool {
	return r != nil && r.permissions[permission]
}

// BearerEnabled reports whether any registered client enables Bearer authentication.
func (r *Registry) BearerEnabled() bool { return r != nil && r.bearer }
