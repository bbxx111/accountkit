package audit

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/auditdb"
	"github.com/google/uuid"
)

var errAuditOwnersChanged = errors.New("audit: flow ownership changed during locking")

type privacyRefs struct{ users, flows []string }

type privacyPlan struct {
	clean   []bool
	cleanup []uuid.UUID
}

func (s *Store) privacyReferences(e Event) privacyRefs {
	r := privacyRefs{}
	if e.Actor.Type == ActorUser && e.Actor.Namespace == s.options.Namespace {
		r.users = append(r.users, e.Actor.ID)
	}
	add := func(resource Resource) {
		if resource.Namespace != s.options.Namespace {
			return
		}
		switch resource.Type {
		case "USER":
			r.users = append(r.users, resource.ID)
		case "IDENTITY_FLOW":
			r.flows = append(r.flows, resource.ID)
		}
	}
	for _, resource := range e.Targets {
		add(resource)
	}
	for _, resource := range e.Context.RelatedResources {
		add(resource)
	}
	return r
}

func sortedPrivacyIDs(ids []string) []string {
	slices.Sort(ids)
	return slices.Compact(ids)
}

// lockPrivacy plans the entire batch before taking any locks. The authoritative
// flow rows are read only after all known users are locked; a new owner requires
// rollback of this attempt, never acquiring a user while holding a flow lock.
func (s *Store) lockPrivacy(ctx context.Context, q *db.Queries, events []Event) (privacyPlan, error) {
	refs := make([]privacyRefs, len(events))
	users, flows := []string{}, []string{}
	associations := map[string][]string{}
	for i, e := range events {
		refs[i] = s.privacyReferences(e)
		users = append(users, refs[i].users...)
		flows = append(flows, refs[i].flows...)
		// Co-occurrence is per event, never a cross product of the whole batch.
		for _, flow := range refs[i].flows {
			associations[flow] = append(associations[flow], refs[i].users...)
		}
	}
	flows = sortedPrivacyIDs(flows)
	historyParams := db.ListAuditPrivacyFlowUsersParams{Source: s.options.Source, Namespace: s.options.Namespace, FlowIds: flows}
	history, err := q.ListAuditPrivacyFlowUsers(ctx, historyParams)
	if err != nil {
		return privacyPlan{}, err
	}
	for _, link := range history {
		users = append(users, link.UserID)
		associations[link.FlowID] = append(associations[link.FlowID], link.UserID)
	}
	snapshot, err := q.SnapshotAuditFlows(ctx, flows)
	if err != nil {
		return privacyPlan{}, err
	}
	for _, f := range snapshot {
		users = append(users, f.ProofUserIds...)
		if f.UserID != nil {
			users = append(users, *f.UserID)
		}
	}
	users = sortedPrivacyIDs(users)
	lockedUsers, err := q.LockAuditUsers(ctx, users)
	if err != nil {
		return privacyPlan{}, err
	}
	states := make(map[string]enum.UserState, len(lockedUsers))
	for _, u := range lockedUsers {
		states[u.ID] = enum.UserState(u.State)
	}
	// A missing flow has no row to lock. Scope locks also serialize history
	// writers when that referenced flow is created for the first time meanwhile.
	keys, err := q.ListAuditFlowLockKeys(ctx, db.ListAuditFlowLockKeysParams{Source: s.options.Source, Namespace: s.options.Namespace, FlowIds: flows})
	if err != nil {
		return privacyPlan{}, err
	}
	for _, key := range keys {
		if err := q.LockAuditFlowScope(ctx, key); err != nil {
			return privacyPlan{}, err
		}
	}
	lockedFlows, err := q.LockAuditFlows(ctx, flows)
	if err != nil {
		return privacyPlan{}, err
	}
	// A competing Store can add historical links without changing live owners.
	// Live flow locks serialize these additions; re-read before any audit lock.
	history, err = q.ListAuditPrivacyFlowUsers(ctx, historyParams)
	if err != nil {
		return privacyPlan{}, err
	}
	for _, link := range history {
		if _, planned := slices.BinarySearch(users, link.UserID); !planned {
			return privacyPlan{}, errAuditOwnersChanged
		}
		associations[link.FlowID] = append(associations[link.FlowID], link.UserID)
	}
	owners := make(map[string][]string, len(lockedFlows))
	expired := make(map[string]bool, len(lockedFlows))
	for _, f := range lockedFlows {
		ids := slices.Clone(f.ProofUserIds)
		if f.UserID != nil {
			ids = append(ids, *f.UserID)
		}
		for _, id := range ids {
			if _, planned := slices.BinarySearch(users, id); !planned {
				return privacyPlan{}, errAuditOwnersChanged
			}
		}
		owners[f.ID] = ids
		expired[f.ID] = !f.ExpireTime.After(time.Now())
	}
	clean := make([]bool, len(events))
	for i, r := range refs {
		affected := slices.Clone(r.users)
		for _, flow := range r.flows {
			ids, exists := owners[flow]
			if !exists || expired[flow] {
				clean[i] = true
			}
			affected = append(affected, ids...)
			affected = append(affected, associations[flow]...)
		}
		for _, user := range affected {
			state, exists := states[user]
			// A missing row cannot be row-locked. Conservatively drop optional
			// privacy for explicit local references; IDs and facts remain intact.
			if !exists || state == enum.UserDeleted {
				clean[i] = true
			}
		}
	}
	// A late direct USER/FLOW link can expand a deleted user's existing privacy
	// closure. Scrub old flow rows in this transaction as well as new events.
	cleanupFlows := []string{}
	for flow, linkedUsers := range associations {
		for _, user := range linkedUsers {
			state, exists := states[user]
			if !exists || state == enum.UserDeleted {
				cleanupFlows = append(cleanupFlows, flow)
				break
			}
		}
	}
	cleanup, err := q.ListAuditPrivacyCleanupIDs(ctx, db.ListAuditPrivacyCleanupIDsParams{Source: s.options.Source, Namespace: s.options.Namespace, FlowIds: sortedPrivacyIDs(cleanupFlows)})
	if err != nil {
		return privacyPlan{}, err
	}
	return privacyPlan{clean: clean, cleanup: cleanup}, nil
}

func scrubStoredEvent(e Event) Event {
	if _, err := enum.ParseEventType(e.Action); err == nil {
		return authentication.Scrub(e)
	}
	return event.Scrub(e)
}
