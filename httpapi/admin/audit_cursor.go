package admin

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/google/uuid"
)

type auditCursor struct {
	Version  int            `json:"v"`
	Source   string         `json:"source"`
	Resource audit.Resource `json:"resource"`
	Time     time.Time      `json:"t"`
	ID       uuid.UUID      `json:"id"`
}

func encodeAuditCursor(q audit.Query, last audit.EventCursor) string {
	b, _ := json.Marshal(auditCursor{Version: 1, Source: q.Source, Resource: q.Resource, Time: last.Time, ID: last.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeAuditCursor(raw string, q audit.Query) (*audit.EventCursor, *apierror.Error) {
	if raw == "" {
		return nil, nil
	}
	bad := apierror.New(apierror.StatusInvalidArgument, "INVALID_PAGE_TOKEN", "page_token is invalid or expired; restart from the first page")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	var c auditCursor
	if json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Source != q.Source || c.Resource != q.Resource || c.Time.IsZero() || c.ID.Version() != 7 || c.ID.Variant() != uuid.RFC4122 {
		return nil, bad
	}
	return &audit.EventCursor{Time: c.Time, ID: c.ID}, nil
}
