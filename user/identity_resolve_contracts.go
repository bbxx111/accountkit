package user

// Stable wire values; keep persisted bytes compatible.
const (
	identityResolveAction      = "IDENTITY_RESOLVE"
	identityResolveResolved    = "resolved"
	identityResolveNotFound    = "not_found"
	identityResolveUnavailable = "unavailable"
	identityResolveInternal    = "internal"
)
