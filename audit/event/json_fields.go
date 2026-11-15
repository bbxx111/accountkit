package event

// Stable wire values; keep persisted bytes compatible.
const (
	fieldId               = "id"
	fieldSource           = "source"
	fieldAction           = "action"
	fieldResult           = "result"
	fieldActor            = "actor"
	fieldTargets          = "targets"
	fieldContext          = "context"
	fieldData             = "data"
	fieldOccurTime        = "occur_time"
	fieldCreateTime       = "create_time"
	fieldType             = "type"
	fieldNamespace        = "namespace"
	fieldName             = "name"
	fieldRequestId        = "request_id"
	fieldTraceId          = "trace_id"
	fieldSpanId           = "span_id"
	fieldClientIp         = "client_ip"
	fieldUserAgent        = "user_agent"
	fieldDeviceId         = "device_id"
	fieldRelatedResources = "related_resources"
)
