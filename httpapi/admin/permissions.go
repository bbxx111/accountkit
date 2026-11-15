package admin

import "net/http"

// Permission identifies one concrete administrator operation.
type Permission string

// Stable administrator permissions; wildcards are policy inputs only.
const (
	PermissionUsersRead         Permission = "users.read"
	PermissionUsersFreeze       Permission = "users.freeze"
	PermissionUsersUnfreeze     Permission = "users.unfreeze"
	PermissionUsersDelete       Permission = "users.delete"
	PermissionUsersUndelete     Permission = "users.undelete"
	PermissionIdentitiesReveal  Permission = "identities.reveal"
	PermissionSessionsRead      Permission = "sessions.read"
	PermissionSessionsRevoke    Permission = "sessions.revoke"
	PermissionSessionsRevokeAll Permission = "sessions.revoke_all"
	PermissionAuditEventsRead   Permission = "audit_events.read"
)

// Permissions returns an independent catalog copy.
func Permissions() []Permission {
	return []Permission{PermissionUsersRead, PermissionUsersFreeze, PermissionUsersUnfreeze, PermissionUsersDelete, PermissionUsersUndelete, PermissionIdentitiesReveal, PermissionSessionsRead, PermissionSessionsRevoke, PermissionSessionsRevokeAll, PermissionAuditEventsRead}
}

// PermissionVerifier optionally replaces legacy role authorization on every route.
type PermissionVerifier interface {
	RequirePermission(Permission) func(http.Handler) http.Handler
}
