package admin

import (
	"bytes"
	"context"
	"net/http"
	"reflect"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/reqid"
	"github.com/google/uuid"
)

func sameAuditResource(a, b audit.Resource) bool {
	return a.Namespace == b.Namespace && a.Type == b.Type && a.ID == b.ID
}

// Validate the stored view as well as the producer event; external Go readers
// need not have passed through RecordView.UnmarshalJSON.
func validatedAuditRecord(record audit.RecordView) (audit.Event, bool) {
	if record.CreateTime.IsZero() || record.CreateTime.UTC().Year() < 1 || record.CreateTime.UTC().Year() > 9999 {
		return audit.Event{}, false
	}
	if _, err := record.CreateTime.MarshalJSON(); err != nil {
		return audit.Event{}, false
	}
	frozen, err := event.Freeze(record.Event)
	return frozen, err == nil
}

// Authentication snapshots name local resources. A same-ID reference from a
// different namespace cannot support that projection. Background sessions may
// be related resources; neither sessions nor flows need to exist currently.
func auditProjectionInNamespace(e audit.Event, projection authentication.Input, namespace string) bool {
	for _, ref := range []audit.Resource{
		{Type: "SESSION", Namespace: namespace, ID: projection.SessionID},
		{Type: "IDENTITY_FLOW", Namespace: namespace, ID: projection.FlowID},
	} {
		if ref.ID != "" && !directAuditResource(e, ref) {
			return false
		}
	}
	return true
}

func auditAfter(e audit.Event, after audit.EventCursor) bool {
	return e.OccurTime.After(after.Time) || (e.OccurTime.Equal(after.Time) && bytes.Compare(e.ID[:], after.ID[:]) > 0)
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
	if e.Source != q.Source {
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
			if link.Flow.Namespace != r.Namespace || link.Flow.Type != r.Type || link.Flow.ID != r.ID || !sameAuditResource(link.User, q.Resource) {
				continue
			}
			if link.Live {
				if !reflect.ValueOf(link.Evidence).IsZero() || h.d.FlowOwner == nil {
					continue
				}
				ok, err := h.d.FlowOwner(ctx, q.Resource.ID, r.ID)
				if err == nil && ok {
					return true
				}
				continue
			}
			ev, valid := validatedAuditRecord(link.Evidence)
			if !valid || ev.Source != q.Source || ev.Result == nil || *ev.Result != enum.ResultSuccess {
				continue
			}
			if ev.Action != "SIGN_IN_COMPLETION_PROOF_VERIFIED" && ev.Action != "SIGN_IN_COMPLETION_COMPLETED" {
				continue
			}
			if projection, err := authentication.Projection(ev); err == nil && auditProjectionInNamespace(ev, projection, q.Resource.Namespace) && directAuditResource(ev, q.Resource) && directAuditResource(ev, r) {
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
	unavailable := func(reason string) {
		h.d.Logger.WarnContext(r.Context(), "audit query unavailable", "reason", reason, "record_count", len(page.Records), "request_id", reqid.From(r.Context()))
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "AUDIT_QUERY_UNAVAILABLE", "audit query unavailable"))
	}
	if err != nil {
		unavailable("reader_error")
		return
	}
	if len(page.Records) > query.Limit {
		unavailable("page_limit")
		return
	}
	projections := make([]authentication.Input, 0, len(page.Records))
	seen := make(map[uuid.UUID]bool, len(page.Records))
	previous := query.After
	for _, record := range page.Records {
		frozen, valid := validatedAuditRecord(record)
		if !valid {
			unavailable("invalid_record")
			return
		}
		if seen[frozen.ID] || (previous != nil && !auditAfter(frozen, *previous)) {
			unavailable("page_order")
			return
		}
		seen[frozen.ID] = true
		previous = &audit.EventCursor{Time: frozen.OccurTime, ID: frozen.ID}
		if !h.auditInScope(r.Context(), frozen, query, page.FlowLinks) {
			unavailable("record_scope")
			return
		}
		projection, err := authentication.Projection(frozen)
		if err != nil || !auditProjectionInNamespace(frozen, projection, query.Resource.Namespace) {
			unavailable("authentication_projection")
			return
		}
		projections = append(projections, projection)
	}
	out := auditEventList{AuditEvents: make([]auditEventResource, 0, min(len(page.Records), size))}
	for i, projection := range projections {
		if i == size {
			last := projections[size-1]
			out.NextPageToken = encodeAuditCursor(query, audit.EventCursor{Time: last.OccurTime, ID: last.ID})
			break
		}
		out.AuditEvents = append(out.AuditEvents, newAuditEventResource(uid, projection))
	}
	apierror.WriteJSON(w, http.StatusOK, out)
}
