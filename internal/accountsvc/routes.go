package accountsvc

import (
	"net/http"

	"github.com/bbxx111/accountkit/internal/accountsvc/identityresolve"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/purgefeed"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

type routeCategory string

const (
	routeConsumer   routeCategory = "consumer"
	routeAdmin      routeCategory = "admin"
	routeService    routeCategory = "service"
	routePublic     routeCategory = "public"
	routeOperations routeCategory = "operations"
)

type routeID uint8

const (
	routeIntrospect routeID = iota
	routePurgeFeed
	routeIdentityResolve
	routeJWKS
	routeEndUser
	routeAdministrator
	routeHealth
	routeReady
	routeMetrics
)

type routeDeclaration struct {
	id   routeID
	path string
	// Empty for delegated consumer/admin mounts. Service handlers enforce methods
	// after authentication; their declarations must not become router.Method calls.
	methods                     string
	category                    routeCategory
	realm, endpoint, permission string
}

// Returning values keeps this internal inventory immutable across callers.
// Categories describe access boundaries, but do not authenticate or isolate networks.
func routeDeclarations() [9]routeDeclaration {
	return [9]routeDeclaration{
		{id: routeIntrospect, path: "/v1/introspect", methods: http.MethodPost, category: routeService, realm: introspection.Realm, endpoint: introspection.Endpoint, permission: serviceauth.PermissionTokensIntrospect},
		{id: routePurgeFeed, path: "/v1/purgedUsers", methods: http.MethodGet, category: routeService, realm: purgefeed.Realm, endpoint: purgefeed.Endpoint, permission: serviceauth.PermissionPurgedUsersRead},
		{id: routeIdentityResolve, path: "/v1/identities:resolve", methods: http.MethodPost, category: routeService, realm: identityresolve.Realm, endpoint: identityresolve.Endpoint, permission: serviceauth.PermissionIdentitiesResolve},
		{id: routeJWKS, path: "/.well-known/jwks.json", methods: "GET, HEAD", category: routePublic},
		{id: routeEndUser, path: "/v1", category: routeConsumer},
		{id: routeAdministrator, path: "/admin/v1", category: routeAdmin},
		{id: routeHealth, path: "/healthz", methods: http.MethodGet, category: routeOperations},
		{id: routeReady, path: "/readyz", methods: http.MethodGet, category: routeOperations},
		{id: routeMetrics, path: "/metrics", methods: http.MethodGet, category: routeOperations},
	}
}

func serviceRoute(id routeID) routeDeclaration {
	for _, route := range routeDeclarations() {
		if route.id == id && route.category == routeService {
			return route
		}
	}
	panic("accountsvc: unknown internal service route")
}
