package audit

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
)

var errAuditOwnersChanged = errors.New("audit: flow ownership changed during locking")

type privacyRefs struct{ users, flows []string }

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
func (s *Store) lockPrivacy(ctx context.Context, q *db.Queries, events []Event) ([]bool, error) {
	refs := make([]privacyRefs, len(events))
	users, flows := []string{}, []string{}
	for i, e := range events {
		refs[i] = s.privacyReferences(e)
		users = append(users, refs[i].users...)
		flows = append(flows, refs[i].flows...)
	}
	flows = sortedPrivacyIDs(flows)
	snapshot, err := q.SnapshotAuditFlows(ctx, flows)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	states := make(map[string]enum.UserState, len(lockedUsers))
	for _, u := range lockedUsers {
		states[u.ID] = enum.UserState(u.State)
	}
	lockedFlows, err := q.LockAuditFlows(ctx, flows)
	if err != nil {
		return nil, err
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
				return nil, errAuditOwnersChanged
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
	return clean, nil
}

func scrubStoredEvent(e Event) Event {
	if _, err := enum.ParseEventType(e.Action); err == nil {
		return authentication.Scrub(e)
	}
	return event.Scrub(e)
}
