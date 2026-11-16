package admin

// HTTP reasons remain distinct from google.rpc statuses and audit reasons.
const (
	reasonAuditQueryNotConfigured = "AUDIT_QUERY_NOT_CONFIGURED"
	reasonAuditQueryUnavailable   = "AUDIT_QUERY_UNAVAILABLE"
	reasonInvalidFilter           = "INVALID_FILTER"
	reasonInvalidShowDeleted      = "INVALID_SHOW_DELETED"
	reasonAdminNotConfigured      = "ADMIN_NOT_CONFIGURED"
	reasonInvalidPageSize         = "INVALID_PAGE_SIZE"
)
