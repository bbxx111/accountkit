package admin

// Local reader-rejection classes are diagnostics, separate from HTTP reasons.
const (
	auditRejectReaderError              = "reader_error"
	auditRejectPageLimit                = "page_limit"
	auditRejectInvalidRecord            = "invalid_record"
	auditRejectPageOrder                = "page_order"
	auditRejectRecordScope              = "record_scope"
	auditRejectAuthenticationProjection = "authentication_projection"
)
