package smtp

// Stable protocol values retain their existing wire bytes.
const (
	mimeSTARTTLS                = "STARTTLS"
	mimeMultipartAlternative    = "multipart/alternative"
	mimeBoundary                = "boundary"
	mimeTextPlain               = "text/plain"
	mimeTextHtml                = "text/html"
	mimeContentType             = "Content-Type"
	mimeContentTransferEncoding = "Content-Transfer-Encoding"
	mimeQuotedPrintable         = "quoted-printable"
)
