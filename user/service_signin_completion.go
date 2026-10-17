package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/jackc/pgx/v5"
)

func (s *Service) findProof(ctx context.Context, q *db.Queries, p completionProof) (db.Identity, error) {
	if p.Kind.IsChannel() {
		return q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: p.Kind, Digests: s.d.Digester.AllDigests(p.Target)})
	}
	if p.Provider == nil || p.Provider.Subject == "" {
		return db.Identity{}, ErrSignInCompletionInvalid
	}
	return q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: p.Kind, ProviderSubject: &p.Provider.Subject})
}

func (s *Service) createProofIdentity(ctx context.Context, q *db.Queries, uid string, p completionProof) (db.Identity, error) {
	if p.Kind.IsChannel() {
		norm, prefix, suffix, err := s.normalizeTarget(p.Kind, p.Target)
		if err != nil {
			return db.Identity{}, err
		}
		return s.createAnchorIdentity(ctx, q, uid, p.Kind, norm, prefix, suffix)
	}
	return s.createProviderIdentity(ctx, q, uid, *p.Provider)
}

// signInVerified 接收已验证证明，在事务内选择已有账号、直接注册或仅保存待补全流程。
func (s *Service) signInVerified(ctx context.Context, proof completionProof, expiry time.Time, dev Device, meta Meta) (SignInResult, error) {
	var result SignInResult
	var replaced []string
	ev := audit.AuthenticationEvent{IdentityKind: proof.Kind, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID}
	if proof.Kind.IsChannel() {
		digest, _ := s.d.Digester.Digest(proof.Target)
		ev.SubjectHint = audit.Hint(digest)
	}
	err := s.withRetryOnUnique(ctx, func(q *db.Queries) error {
		result = SignInResult{}
		replaced = nil
		found, err := s.findProof(ctx, q, proof)
		var u db.UserAccount
		isNew := false
		switch {
		case err == nil:
			u, err = q.LockUserByID(ctx, found.UserID)
			if err != nil {
				return err
			}
			ev.UserID = u.ID
			current, e := s.findProof(ctx, q, proof)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if e != nil || current.ID != found.ID || current.UserID != u.ID {
				if proof.Kind.IsChannel() {
					return code.ErrInvalid
				}
				return idp.ErrInvalidCredential
			}
			ev.VerifiedSubject = true
			ev.IdentityID = current.ID
			if !proof.Kind.IsChannel() {
				if u.State == enum.UserPendingDeletion {
					return ErrUserPendingDeletion
				}
				if err = s.mergeOpenIDs(ctx, q, current, proof.Provider.OpenIDs, s.now()); err != nil {
					return err
				}
			}
		case errors.Is(err, pgx.ErrNoRows):
			if !s.policy.Complete([]enum.IdentityKind{proof.Kind}) {
				result, ev.FlowID, err = s.createPending(ctx, q, proof, expiry, dev)
				return err
			}
			uid, e := ids.New(ids.User)
			if e != nil {
				return e
			}
			u, err = q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
			if err != nil {
				return err
			}
			if _, err = s.createProofIdentity(ctx, q, uid, proof); err != nil {
				return err
			}
			isNew = true
		default:
			return err
		}
		complete, err := s.userIdentityComplete(ctx, q, u.ID)
		if err != nil {
			return err
		}
		sess, pair, rep, err := s.establishSession(ctx, q, u, complete, dev, s.now())
		if err != nil {
			return err
		}
		pair.IsNewUser = isNew
		if proof.Provider != nil {
			pair.HintEmail = proof.Provider.HintEmail
		}
		result = SignInResult{Tokens: &pair}
		replaced = rep
		ev.UserID, ev.SessionID = u.ID, sess.ID
		return nil
	})
	if err != nil {
		if reason := signInRejectReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return SignInResult{}, err
	}
	if result.Tokens != nil {
		s.finishSignIn(ctx, ev, *result.Tokens, replaced, dev, meta)
	} else {
		ev.Type, ev.Result = enum.EventSignInCompletionStarted, enum.ResultSuccess
		s.record(ctx, ev)
	}
	return result, nil
}

// SendSignInCompletionCode 仅向本流程发送 PHONE/EMAIL 补全验证码，不预查询目标归属。
func (s *Service) SendSignInCompletionCode(ctx context.Context, token string, channel enum.IdentityKind, target string, dev Device, meta Meta) (result CodeChallenge, err error) {
	var flowID string
	defer func() {
		err = flowStorageError(err)
		if err != nil {
			s.recordCompletionRejected(ctx, token, flowID, channel, dev, meta, err)
		}
	}()
	if !channel.IsChannel() {
		return CodeChallenge{}, ErrInvalidArgument
	}
	if err := s.requireIdentityEnabled(channel); err != nil {
		return CodeChallenge{}, err
	}
	row, _, err := s.loadFlow(ctx, token, dev)
	flowID = row.ID
	if err != nil {
		return CodeChallenge{}, err
	}
	if row.State == enum.FlowCompleted {
		return CodeChallenge{}, ErrSignInAlreadyCompleted
	}
	result, err = s.sendCodeUntil(ctx, channel, enum.PurposeSignInComplete, target, code.Binding{FlowID: row.ID}, meta, row.ExpireTime)
	if errors.Is(err, code.ErrExpired) {
		// Redis may execute after the ticket deadline. Preserve CODE_EXPIRED
		// only when the ticket itself is still valid.
		if _, _, flowErr := s.loadFlow(ctx, token, dev); flowErr != nil {
			return CodeChallenge{}, flowErr
		}
	}
	return result, err
}

// CompleteSignIn 先持久化成功证明，再由独立事务完成账号、身份与会话。
func (s *Service) CompleteSignIn(ctx context.Context, token string, cred CodeCredential, dev Device, meta Meta) (result SignInResult, err error) {
	var flowID string
	defer func() {
		err = flowStorageError(err)
		if err != nil {
			s.recordCompletionRejected(ctx, token, flowID, cred.Channel, dev, meta, err)
		}
	}()
	if err := validateCodeCredential(cred); err != nil {
		return SignInResult{}, err
	}
	if err := s.requireIdentityEnabled(cred.Channel); err != nil {
		return SignInResult{}, err
	}
	norm, _, _, err := s.normalizeTarget(cred.Channel, cred.Target)
	if err != nil {
		return SignInResult{}, err
	}
	cred.Target = norm
	request := completionRequest(token, cred)
	row, p, err := s.loadFlow(ctx, token, dev)
	flowID = row.ID
	if err != nil {
		return SignInResult{}, err
	}
	if row.State != enum.FlowCompleted && !slices.Contains(p.Requests, request) {
		// The ticket is checked before Redis consumption and again under SQL locks.
		_, err = s.d.Codes.VerifyChallengeWithExpiry(ctx, code.Credential{Channel: cred.Channel, Purpose: enum.PurposeSignInComplete, Target: norm, CodeID: cred.CodeID, Code: cred.Code, Binding: code.Binding{FlowID: row.ID}})
		if err != nil {
			if errors.Is(err, code.ErrUnavailable) {
				return SignInResult{}, ErrUnavailable
			}
			// A simultaneous identical request may have committed after our initial read.
			fresh, fp, e := s.loadFlow(ctx, token, dev)
			if e != nil {
				return SignInResult{}, e
			}
			if slices.Contains(fp.Requests, request) {
				return s.finalizeSignIn(ctx, token, fresh, fp, request, dev, meta)
			}
			return SignInResult{}, err
		}
		row, p, err = s.persistCompletionProof(ctx, row, p, completionProof{Kind: cred.Channel, Target: norm}, request, dev, meta)
		if err != nil {
			return SignInResult{}, err
		}
	}
	return s.finalizeSignIn(ctx, token, row, p, request, dev, meta)
}

func proofOwnerIDs(p completionPayload) []string {
	out := []string{}
	for _, proof := range p.Proofs {
		if proof.UserID != "" {
			out = append(out, proof.UserID)
		}
	}
	if p.Result != nil {
		out = append(out, p.Result.UserID)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (s *Service) lockProofOwners(ctx context.Context, q *db.Queries, p completionPayload) (map[string]db.UserAccount, error) {
	locked := map[string]db.UserAccount{}
	for _, uid := range proofOwnerIDs(p) {
		u, err := q.LockUserByID(ctx, uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSignInCompletionInvalid
		}
		if err != nil {
			return nil, err
		}
		locked[uid] = u
	}
	return locked, nil
}

func (s *Service) checkProofOwners(ctx context.Context, q *db.Queries, p completionPayload, locked map[string]db.UserAccount) error {
	for _, proof := range p.Proofs {
		current, err := s.findProof(ctx, q, proof)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if proof.UserID == "" {
			if err == nil {
				return ErrIdentityConflict
			}
			continue
		}
		if err != nil || current.UserID != proof.UserID || current.ID != proof.IdentityID {
			return ErrSignInCompletionInvalid
		}
		u, ok := locked[proof.UserID]
		if !ok {
			return ErrSignInCompletionInvalid
		}
		if err := completionAccountState(u); err != nil {
			return err
		}
	}
	return nil
}

func completionAccountState(u db.UserAccount) error {
	switch u.State {
	case enum.UserActive:
		return nil
	case enum.UserFrozen:
		return ErrUserFrozen
	case enum.UserPendingDeletion:
		return ErrUserPendingDeletion
	default:
		return ErrSignInCompletionInvalid
	}
}

func sameProof(a, b completionProof) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind.IsChannel() {
		return a.Target == b.Target
	}
	return a.Provider != nil && b.Provider != nil && a.Provider.Subject == b.Provider.Subject
}

func (s *Service) persistCompletionProof(ctx context.Context, row db.IdentityFlow, p completionPayload, proof completionProof, request string, dev Device, meta Meta) (db.IdentityFlow, completionPayload, error) {
	// Only the newly verified target may discover an existing owner here. Earlier
	// proofs retain their original ownership snapshot throughout the flow.
	current, err := s.findProof(ctx, s.d.Repo.Q(), proof)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return row, p, err
	}
	if err == nil {
		proof.UserID, proof.IdentityID = current.UserID, current.ID
	}
	locks := p
	locks.Proofs = append(slices.Clone(p.Proofs), proof)
	persisted := false
	err = s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		owners, e := s.lockProofOwners(ctx, q, locks)
		if e != nil {
			return e
		}
		fresh, e := q.LockIdentityFlow(ctx, row.ID)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrSignInCompletionInvalid
		}
		if e != nil {
			return e
		}
		fp, e := s.decodeFlow(fresh, dev)
		if e != nil {
			return e
		}
		if slices.Contains(fp.Requests, request) {
			row, p = fresh, fp
			return nil
		}
		if fresh.State == enum.FlowCompleted {
			return ErrSignInAlreadyCompleted
		}
		if fresh.Revision != row.Revision {
			return ErrSignInCompletionInvalid
		}
		if e = s.checkProofOwners(ctx, q, locks, owners); e != nil {
			return e
		}
		duplicate := false
		for _, old := range fp.Proofs {
			if sameProof(old, proof) {
				duplicate = true
			}
		}
		if !duplicate {
			fp.Proofs = append(fp.Proofs, proof)
		}
		fp.Requests = append(fp.Requests, request)
		ct, v, e := s.encodeFlow(row.ID, fp)
		if e != nil {
			return e
		}
		fresh, e = q.UpdateIdentityFlow(ctx, db.UpdateIdentityFlowParams{ID: row.ID, Revision: fresh.Revision, State: enum.FlowVerified, Payload: ct, CipherVersion: v, ProofUserIds: proofOwnerIDs(fp), Now: s.now()})
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrSignInCompletionInvalid
		}
		if e != nil {
			return e
		}
		row, p = fresh, fp
		persisted = true
		return nil
	})
	if err == nil && persisted {
		s.record(ctx, audit.AuthenticationEvent{Type: enum.EventSignInCompletionProofVerified, Result: enum.ResultSuccess, FlowID: row.ID, UserID: proof.UserID, VerifiedSubject: proof.UserID != "", IdentityID: proof.IdentityID, IdentityKind: proof.Kind, IP: meta.IP, DeviceID: p.Device.ID, RequestID: meta.RequestID})
	}
	return row, p, err
}

func (s *Service) replayCompletion(ctx context.Context, q *db.Queries, row db.IdentityFlow, p completionPayload, request string, owners map[string]db.UserAccount) (SignInResult, error) {
	if p.Result == nil || row.UserID == nil || row.SessionID == nil || p.FinalRequest != request {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	u, ok := owners[*row.UserID]
	if !ok || u.State != enum.UserActive {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	sess, err := q.LockSessionByID(ctx, *row.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	if err != nil {
		return SignInResult{}, err
	}
	if !row.ExpireTime.After(s.now()) {
		return SignInResult{}, ErrSignInCompletionInvalid
	}
	if sess.UserID != u.ID || !sessionActiveAt(sess, s.now()) || sess.RevokeTime != nil || !bytes.Equal(sess.RefreshTokenHash, hashRefresh(p.Result.RefreshToken)) || !p.AccessExpireTime.After(s.now()) {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	claims, err := s.d.Signer.Parse(p.Result.AccessToken)
	if err != nil || claims.UserID != u.ID || claims.SessionID != sess.ID {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	revoked, err := s.d.Revocation.IsRevoked(ctx, sess.ID)
	if err != nil {
		return SignInResult{}, ErrUnavailable
	}
	if revoked {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	now := s.now()
	if !row.ExpireTime.After(now) {
		return SignInResult{}, ErrSignInCompletionInvalid
	}
	if !p.AccessExpireTime.After(now) || !sessionActiveAt(sess, now) {
		return SignInResult{}, ErrSignInAlreadyCompleted
	}
	return SignInResult{Tokens: p.Result}, nil
}

func (s *Service) finalizeSignIn(ctx context.Context, token string, row db.IdentityFlow, p completionPayload, request string, dev Device, meta Meta) (SignInResult, error) {
	var result SignInResult
	var replaced []string
	var bound []db.Identity
	var event audit.AuthenticationEvent
	committed := false
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		owners, err := s.lockProofOwners(ctx, q, p)
		if err != nil {
			return err
		}
		fresh, err := q.LockIdentityFlow(ctx, row.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSignInCompletionInvalid
		}
		if err != nil {
			return err
		}
		fp, err := s.decodeFlow(fresh, dev)
		if err != nil {
			return err
		}
		if fresh.State == enum.FlowCompleted {
			// A concurrent completion may have discovered a user not in our lock set.
			// Retry from a fresh snapshot outside this transaction; never invert locks.
			if fresh.UserID != nil {
				if _, ok := owners[*fresh.UserID]; !ok {
					return errCompletionRetry
				}
			}
			result, err = s.replayCompletion(ctx, q, fresh, fp, request, owners)
			return err
		}
		if fresh.Revision != row.Revision || !slices.Contains(fp.Requests, request) {
			return ErrSignInCompletionInvalid
		}
		if err = s.checkProofOwners(ctx, q, fp, owners); err != nil {
			return err
		}
		if len(owners) > 1 {
			return ErrIdentityConflict
		}
		var kinds []enum.IdentityKind
		for _, proof := range fp.Proofs {
			kinds = append(kinds, proof.Kind)
		}
		if len(owners) == 0 && !s.policy.Complete(kinds) {
			result = s.pendingResult(token, fresh.ExpireTime)
			return nil
		}
		var u db.UserAccount
		isNew := len(owners) == 0
		if isNew {
			uid, e := ids.New(ids.User)
			if e != nil {
				return e
			}
			u, err = q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
			if err != nil {
				return err
			}
		} else {
			for _, owner := range owners {
				u = owner
			}
		}
		if err = completionAccountState(u); err != nil {
			return err
		}
		idents, err := q.ListActiveIdentitiesByUser(ctx, u.ID)
		if err != nil {
			return err
		}
		for _, proof := range fp.Proofs {
			if proof.UserID != "" {
				if proof.Provider != nil {
					existing, e := s.findProof(ctx, q, proof)
					if e != nil {
						return e
					}
					if e = s.mergeOpenIDs(ctx, q, existing, proof.Provider.OpenIDs, s.now()); e != nil {
						return e
					}
				}
				continue
			}
			if countKind(idents, proof.Kind) >= s.d.MaxIdentitiesPerKind {
				return ErrIdentityKindLimit
			}
			created, e := s.createProofIdentity(ctx, q, u.ID, proof)
			if e != nil {
				return e
			}
			idents = append(idents, created)
			bound = append(bound, created)
		}
		now := s.now()
		sess, pair, rep, err := s.establishSession(ctx, q, u, s.identitiesComplete(idents), fp.Device, now)
		if err != nil {
			return err
		}
		pair.IsNewUser = isNew
		if fp.Proofs[0].Provider != nil {
			pair.HintEmail = fp.Proofs[0].Provider.HintEmail
		}
		fp.Result = &pair
		fp.FinalRequest = request
		fp.AccessExpireTime = now.Truncate(time.Second).Add(s.d.AccessTTL)
		ct, v, err := s.encodeFlow(fresh.ID, fp)
		if err != nil {
			return err
		}
		_, err = q.CompleteIdentityFlow(ctx, db.CompleteIdentityFlowParams{ID: fresh.ID, Revision: fresh.Revision, Payload: ct, CipherVersion: v, ProofUserIds: proofOwnerIDs(fp), UserID: u.ID, SessionID: sess.ID, Now: s.now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSignInCompletionInvalid
		}
		if err != nil {
			return err
		}
		result = SignInResult{Tokens: &pair}
		replaced = rep
		event = audit.AuthenticationEvent{FlowID: fresh.ID, UserID: u.ID, SessionID: sess.ID, IdentityKind: fp.Proofs[0].Kind, IP: meta.IP, DeviceID: fp.Device.ID, RequestID: meta.RequestID}
		committed = true
		return nil
	})
	if errors.Is(err, errCompletionRetry) {
		fresh, fp, e := s.loadFlow(ctx, token, dev)
		if e != nil {
			return SignInResult{}, e
		}
		return s.finalizeSignIn(ctx, token, fresh, fp, request, dev, meta)
	}
	if IsUniqueViolation(err) {
		return SignInResult{}, ErrIdentityConflict
	}
	if err != nil {
		return SignInResult{}, err
	}
	if committed {
		for _, identity := range bound {
			s.record(ctx, audit.AuthenticationEvent{Type: enum.EventIdentityBound, Result: enum.ResultSuccess, FlowID: event.FlowID, UserID: event.UserID, IdentityID: identity.ID, IdentityKind: identity.Kind, IP: meta.IP, DeviceID: p.Device.ID, RequestID: meta.RequestID})
		}
		event.Type, event.Result = enum.EventSignInCompletionCompleted, enum.ResultSuccess
		s.record(ctx, event)
		s.finishSignIn(ctx, event, *result.Tokens, replaced, p.Device, meta)
	}
	return result, nil
}

var errCompletionRetry = fmt.Errorf("user: completion needs fresh ownership snapshot")
