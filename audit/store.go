package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/internal/auditconfig"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"slices"
	"time"
)

// RetentionBatchSize 是 DeleteOlderThan 每批删除的事件行数。
const RetentionBatchSize = 1000

// ErrConflict 表示同一事件 ID 已保存了不同的事件内容。
var ErrConflict = errors.New("audit: event identity conflict")

// ErrInvalid 表示事件未通过校验，或查询来源、资源范围与分页参数不合法。
var ErrInvalid = errors.New("audit: invalid event or scope")

// StoreOptions 是 authentication.Options 的别名：Store 只读写该来源与资源命名空间内的事件。
type StoreOptions = authentication.Options

// Store 是 audit_event 表上的本地存储，实现 Inserter 与 Reader。
type Store struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	options StoreOptions
}

// NewStore selects local-only source identifiers from pool configuration, without I/O.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: db.New(pool), options: auditconfig.Local(pool)}
}

// NewStoreWithOptions 以显式来源与资源命名空间构造 Store，不做 I/O；配置不合法时返回错误。
func NewStoreWithOptions(pool *pgxpool.Pool, o StoreOptions) (*Store, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return &Store{pool: pool, q: db.New(pool), options: o}, nil
}

// InsertBatch preserves producer identities and rolls back all writes on any error.
func (s *Store) InsertBatch(ctx context.Context, events []Event) (BatchResult, error) {
	if len(events) == 0 {
		return BatchResult{}, nil
	}
	frozen := make([]Event, len(events))
	invalid := 0
	for i, e := range events {
		var err error
		frozen[i], err = event.Freeze(e)
		if err != nil || e.Source != s.options.Source {
			invalid++
		}
	}
	if invalid > 0 {
		return BatchResult{Invalid: invalid, Failed: len(events) - invalid}, ErrInvalid
	}
	// Overlapping batches must reach unique audit keys in the same order too.
	slices.SortStableFunc(frozen, func(a, b Event) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	for range 4 {
		result, err := s.insertAttempt(ctx, frozen)
		if !errors.Is(err, errAuditOwnersChanged) {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return BatchResult{Failed: len(events)}, err
		}
	}
	return BatchResult{Failed: len(events)}, errAuditOwnersChanged
}

func (s *Store) insertAttempt(ctx context.Context, events []Event) (BatchResult, error) {
	// Rechecking links after row locks needs a fresh statement snapshot even
	// when the host configures a repeatable-read default on its connection pool.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return BatchResult{Failed: len(events)}, err
	}
	defer tx.Rollback(context.Background())
	q := db.New(tx)
	plan, err := s.lockPrivacy(ctx, q, events)
	if err != nil {
		return BatchResult{Failed: len(events)}, err
	}
	result := BatchResult{}
	// Merge old-row cleanup and new writes into one UUID lock order. Locking
	// all cleanup rows first could take a high UUID before inserting a low one.
	for i, j := 0, 0; i < len(events) || j < len(plan.cleanup); {
		var id uuid.UUID
		if i < len(events) && (j == len(plan.cleanup) || bytes.Compare(events[i].ID[:], plan.cleanup[j][:]) <= 0) {
			id = events[i].ID
		} else {
			id = plan.cleanup[j]
		}
		for i < len(events) && events[i].ID == id {
			written, err := s.insertEvent(ctx, q, events[i], plan.clean[i])
			if errors.Is(err, ErrConflict) {
				return BatchResult{Conflicts: 1, Failed: len(events) - 1}, err
			}
			if err != nil {
				return BatchResult{Failed: len(events)}, err
			}
			if written {
				result.Written++
			} else {
				result.Duplicate++
			}
			i++
		}
		if j < len(plan.cleanup) && plan.cleanup[j] == id {
			if err := scrubExistingAuditEvent(ctx, q, id); err != nil {
				return BatchResult{Failed: len(events)}, err
			}
			j++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BatchResult{Failed: len(events)}, err
	}
	return result, nil
}

func (s *Store) insertEvent(ctx context.Context, q *db.Queries, e Event, clean bool) (bool, error) {
	incoming := e
	if clean {
		var err error
		incoming, err = event.Freeze(scrubStoredEvent(e))
		if err != nil {
			return false, ErrInvalid
		}
	}
	n, err := q.InsertAuditEvent(ctx, toParams(incoming))
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	row, err := q.GetAuditEvent(ctx, e.ID)
	if err != nil {
		return false, err
	}
	old, err := recordView(row)
	if err != nil {
		return false, err
	}
	same := event.Equivalent(old.Event, e)
	// Existing rows still win; only the controlled privacy projection can differ.
	if !same && old.Source == s.options.Source {
		if event.Equivalent(old.Event, scrubStoredEvent(old.Event)) {
			same = event.Equivalent(old.Event, scrubStoredEvent(e))
		}
	}
	if !same {
		return false, ErrConflict
	}
	return false, nil
}

func scrubExistingAuditEvent(ctx context.Context, q *db.Queries, id uuid.UUID) error {
	row, err := q.LockAuditEvent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // retention can remove a candidate
	}
	if err != nil {
		return err
	}
	old, err := recordView(row)
	if err != nil {
		return err
	}
	clean, err := event.Freeze(scrubStoredEvent(old.Event))
	if err != nil {
		return ErrInvalid
	}
	if event.Equivalent(old.Event, clean) {
		return nil
	}
	p := toParams(clean)
	return q.ScrubAuditEventPrivacy(ctx, db.ScrubAuditEventPrivacyParams{ID: id, Actor: p.Actor, Targets: p.Targets, Context: p.Context, Data: p.Data})
}
func toParams(e Event) db.InsertAuditEventParams {
	actor, _ := json.Marshal(e.Actor)
	targets, _ := json.Marshal(e.Targets)
	contextJSON, _ := json.Marshal(e.Context)
	var result *int16
	if e.Result != nil {
		n := int16(*e.Result)
		result = &n
	}
	return db.InsertAuditEventParams{ID: e.ID, Source: e.Source, Action: e.Action, Result: result, Actor: actor, Targets: targets, Context: contextJSON, Data: e.Data, OccurTime: e.OccurTime}
}
func recordView(r db.AuditEvent) (RecordView, error) {
	e := Event{ID: r.ID, Source: r.Source, Action: r.Action, Result: r.Result, Data: r.Data, OccurTime: r.OccurTime}
	if json.Unmarshal(r.Actor, &e.Actor) != nil || json.Unmarshal(r.Targets, &e.Targets) != nil || json.Unmarshal(r.Context, &e.Context) != nil {
		return RecordView{}, ErrInvalid
	}
	e, err := event.Freeze(e)
	if err != nil {
		return RecordView{}, ErrInvalid
	}
	return RecordView{Event: e, CreateTime: r.CreateTime}, nil
}

// ListByResource 实现 Reader：只返回本 Store 来源与命名空间内指定资源的记录，按 occur_time、id 分页。
func (s *Store) ListByResource(ctx context.Context, query Query) (Page, error) {
	if query.Source != s.options.Source || query.Resource.Namespace != s.options.Namespace || query.Resource.ID == "" || query.Resource.Type == "" || query.Limit < 1 || query.Limit > 1001 {
		return Page{}, ErrInvalid
	}
	actions, err := query.NormalizedActions()
	if err != nil {
		return Page{}, ErrInvalid
	}
	r := query.Resource
	r.Name = ""
	resource, _ := json.Marshal([]Resource{r})
	page := Page{Records: []RecordView{}}
	p := db.ListAuditEventsByResourceParams{Source: query.Source, Actions: actions, ResourceType: r.Type, Namespace: r.Namespace, ResourceID: r.ID, Resource: resource, PageLimit: int32(query.Limit)}
	if query.After != nil {
		p.AfterTime = &query.After.Time
		p.AfterID = &query.After.ID
	}
	rows, err := s.q.ListAuditEventsByResource(ctx, p)
	if err != nil {
		return Page{}, err
	}
	for _, row := range rows {
		record, err := recordView(row)
		if err != nil {
			return Page{}, err
		}
		page.Records = append(page.Records, record)
	}
	if r.Type == "USER" {
		page.FlowLinks, err = s.pageFlowLinks(ctx, query.Source, r, resource, page.Records)
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

// pageFlowLinks returns at most one independently checkable link per stable flow
// reference. Failed authentication projections advance a UUID cursor, in batches
// across the unresolved flows; page actions/times never constrain the evidence.
func (s *Store) pageFlowLinks(ctx context.Context, source string, user Resource, resource []byte, records []RecordView) ([]FlowLink, error) {
	type flowCursor struct {
		ID      string     `json:"id"`
		AfterID *uuid.UUID `json:"after_id,omitempty"`
	}
	seen := map[string]bool{}
	var ids []string
	for _, record := range records {
		for _, refs := range [][]Resource{record.Targets, record.Context.RelatedResources} {
			for _, ref := range refs {
				if ref.Type == "IDENTITY_FLOW" && ref.Namespace == user.Namespace && !seen[ref.ID] {
					seen[ref.ID] = true
					ids = append(ids, ref.ID)
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	slices.Sort(ids)
	pending := make([]flowCursor, len(ids))
	for i, id := range ids {
		pending[i].ID = id
	}
	links := map[string]FlowLink{}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cursors, err := json.Marshal(pending)
		if err != nil {
			return nil, err
		}
		rows, err := s.q.ListAuditFlowEvidence(ctx, db.ListAuditFlowEvidenceParams{Source: source, Namespace: user.Namespace, UserID: user.ID, Resource: resource, FlowCursors: cursors})
		if err != nil {
			return nil, err
		}
		pending = nil
		for _, row := range rows {
			record, err := recordView(db.AuditEvent{ID: row.ID, Source: row.Source, Action: row.Action, Result: row.Result, Actor: row.Actor, Targets: row.Targets, Context: row.Context, Data: row.Data, OccurTime: row.OccurTime, CreateTime: row.CreateTime})
			if err != nil {
				return nil, err
			}
			if validFlowEvidence(record.Event, user.Namespace) {
				links[row.FlowID] = FlowLink{Flow: Resource{Type: "IDENTITY_FLOW", Namespace: user.Namespace, ID: row.FlowID}, User: user, Evidence: record}
			} else {
				id := row.ID
				pending = append(pending, flowCursor{ID: row.FlowID, AfterID: &id})
			}
		}
	}
	var missing []string
	for _, id := range ids {
		if _, ok := links[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		live, err := s.q.ListLiveAuditFlows(ctx, db.ListLiveAuditFlowsParams{FlowIds: missing, UserID: user.ID})
		if err != nil {
			return nil, err
		}
		for _, id := range live {
			links[id] = FlowLink{Flow: Resource{Type: "IDENTITY_FLOW", Namespace: user.Namespace, ID: id}, User: user, Live: true}
		}
	}
	out := make([]FlowLink, 0, len(links))
	for _, id := range ids {
		if link, ok := links[id]; ok {
			out = append(out, link)
		}
	}
	return out, nil
}

// Keep the projection/namespace rules aligned with the admin adapter, which still
// independently checks every returned link before exposing a flow-only record.
func validFlowEvidence(e Event, namespace string) bool {
	projection, err := authentication.Projection(e)
	if err != nil {
		return false
	}
	for _, ref := range []Resource{{Type: "SESSION", Namespace: namespace, ID: projection.SessionID}, {Type: "IDENTITY_FLOW", Namespace: namespace, ID: projection.FlowID}} {
		if ref.ID == "" {
			continue
		}
		found := false
		for _, refs := range [][]Resource{e.Targets, e.Context.RelatedResources} {
			for _, r := range refs {
				if r.Type == ref.Type && r.Namespace == ref.Namespace && r.ID == ref.ID {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ListByUser is a local convenience; integrations should use the explicit Reader scope.
func (s *Store) ListByUser(ctx context.Context, userID string, after *EventCursor, limit int) ([]RecordView, error) {
	page, err := s.ListByResource(ctx, Query{Source: s.options.Source, Resource: Resource{Type: "USER", Namespace: s.options.Namespace, ID: userID}, After: after, Limit: limit})
	return page.Records, err
}

// DeleteOlderThan 分批删除本来源中 occur_time 早于 before 的事件，删到一批不满为止，返回删除总数。
func (s *Store) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.q.DeleteAuditEventsBefore(ctx, db.DeleteAuditEventsBeforeParams{Source: s.options.Source, Before: before, BatchSize: RetentionBatchSize})
		if err != nil {
			return total, err
		}
		total += n
		if n < RetentionBatchSize {
			return total, nil
		}
	}
}
