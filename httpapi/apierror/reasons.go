package apierror

// HTTP reasons remain distinct from google.rpc statuses and audit reasons.
const (
	reasonRouteNotFound    = "ROUTE_NOT_FOUND"
	reasonMethodNotAllowed = "METHOD_NOT_ALLOWED"
)
