package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/google/uuid"
	"net/http"
	"time"
)

type auditCursor struct {
	Version  int            `json:"v"`
	Source   string         `json:"source"`
	Resource audit.Resource `json:"resource"`
	Time     time.Time      `json:"t"`
	ID       uuid.UUID      `json:"id"`
}

func encodeAuditCursor(q audit.Query, last audit.RecordView) string {
	b, _ := json.Marshal(auditCursor{Version: 1, Source: q.Source, Resource: q.Resource, Time: last.OccurTime, ID: last.ID})
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
func directAuditResource(e audit.Event, r audit.Resource) bool {
	if r.Type == "USER" && e.Actor.Type == audit.ActorUser && e.Actor.Namespace == r.Namespace && e.Actor.ID == r.ID {
		return true
	}
	for _, v := range append(append([]audit.Resource{}, e.Targets...), e.Context.RelatedResources...) {
		if v.Namespace == r.Namespace && v.Type == r.Type && v.ID == r.ID {
			return true
		}
	}
	return false
}
func (h *Handler) auditInScope(ctx context.Context, e audit.Event, q audit.Query, links []audit.FlowLink) bool {
	if e.Source != q.Source || event.Validate(e) != nil {
		return false
	}
	if directAuditResource(e, q.Resource) {
		return true
	}
	// Flow closure applies only to early events lacking any explicit consumer owner.
	if e.Actor.Type == audit.ActorUser && e.Actor.Namespace == q.Resource.Namespace {
		return false
	}
	refs := append(append([]audit.Resource{}, e.Targets...), e.Context.RelatedResources...)
	for _, r := range refs {
		if r.Type == "USER" && r.Namespace == q.Resource.Namespace {
			return false
		}
	}
	for _, r := range refs {
		if r.Type != "IDENTITY_FLOW" || r.Namespace != q.Resource.Namespace {
			continue
		}
		for _, link := range links {
			if link.Flow.Namespace != r.Namespace || link.Flow.Type != r.Type || link.Flow.ID != r.ID || link.User != q.Resource {
				continue
			}
			if link.Live {
				if link.Evidence.ID != uuid.Nil || h.d.FlowOwner == nil {
					continue
				}
				ok, err := h.d.FlowOwner(ctx, q.Resource.ID, r.ID)
				if err == nil && ok {
					return true
				}
				continue
			}
			ev := link.Evidence.Event
			if ev.Source != q.Source || ev.Result == nil || *ev.Result != enum.ResultSuccess || event.Validate(ev) != nil {
				continue
			}
			if ev.Action != "SIGN_IN_COMPLETION_PROOF_VERIFIED" && ev.Action != "SIGN_IN_COMPLETION_COMPLETED" {
				continue
			}
			if _, err := authentication.Projection(ev); err == nil && directAuditResource(ev, q.Resource) && directAuditResource(ev, r) {
				return true
			}
		}
	}
	return false
}
func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	uid, ok := userID(w, r)
	if !ok {
		return
	}
	size, e := parsePageSize(r.URL.Query().Get("page_size"))
	if e != nil {
		apierror.Write(w, e)
		return
	}
	query := audit.Query{Source: h.d.AuditSource, Resource: audit.Resource{Type: "USER", Namespace: h.d.AuditNamespace, ID: uid}, Limit: size + 1}
	after, e := decodeAuditCursor(r.URL.Query().Get("page_token"), query)
	if e != nil {
		apierror.Write(w, e)
		return
	}
	query.After = after
	if err := h.d.Users.UserExists(r.Context(), uid); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if h.d.Audit == nil {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "AUDIT_QUERY_NOT_CONFIGURED", "audit queries are not configured"))
		return
	}
	page, err := h.d.Audit.ListByResource(r.Context(), query)
	unavailable := func() {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "AUDIT_QUERY_UNAVAILABLE", "audit query unavailable"))
	}
	if err != nil || len(page.Records) > size+1 {
		unavailable()
		return
	}
	for _, record := range page.Records {
		if !h.auditInScope(r.Context(), record.Event, query, page.FlowLinks) {
			unavailable()
			return
		}
		if _, err := authentication.Projection(record.Event); err != nil {
			unavailable()
			return
		}
	}
	out := auditEventList{AuditEvents: make([]auditEventResource, 0, min(len(page.Records), size))}
	for i, record := range page.Records {
		if i == size {
			out.NextPageToken = encodeAuditCursor(query, page.Records[size-1])
			break
		}
		out.AuditEvents = append(out.AuditEvents, newAuditEventResource(uid, record))
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}
