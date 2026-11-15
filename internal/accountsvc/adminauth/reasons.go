package adminauth

// HTTP reasons remain distinct from google.rpc statuses and audit reasons.
const (
	reasonAdminAuthUnavailable = "ADMIN_AUTH_UNAVAILABLE"
	reasonAdminForbidden       = "ADMIN_FORBIDDEN"
	reasonAdminUnauthenticated = "ADMIN_UNAUTHENTICATED"
)
