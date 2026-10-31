package user

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/userdb"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const signInCompletionTTL = 5 * time.Minute

func flowStorageError(err error) error {
	if err == nil {
		return nil
	}
	var connect *pgconn.ConnectError
	var network net.Error
	var database *pgconn.PgError
	if errors.As(err, &connect) || errors.As(err, &network) ||
		(errors.As(err, &database) && (strings.HasPrefix(database.Code, "08") || strings.HasPrefix(database.Code, "53") || database.Code == "57P01" || database.Code == "57P02" || database.Code == "57P03")) {
		return fmt.Errorf("%w: sign-in flow storage: %w", ErrUnavailable, err)
	}
	return err
}

type completionProof struct {
	Kind       enum.IdentityKind
	Target     string
	Provider   *idp.Identity
	UserID     string
	IdentityID string
}

type completionPayload struct {
	InitialClientInfo            *ClientInfo `json:",omitempty"`
	InitialClientInfoObserveTime *time.Time  `json:",omitempty"`
	Device                       Device
	Proofs                       []completionProof
	Requests                     []string
	FinalRequest                 string
	Result                       *TokenResult
	AccessExpireTime             time.Time
}

func fingerprintJSON(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func signInCompletionFlowConfiguration(d Deps, policy *IdentityPolicy, providers map[enum.IdentityKind]idp.Provider) (string, string) {
	var searchPath string
	if d.Repo.pool != nil {
		cfg := d.Repo.pool.Config().ConnConfig
		first, _, _ := strings.Cut(cfg.RuntimeParams["search_path"], ",")
		searchPath = strings.Trim(strings.TrimSpace(first), `"`)
	}
	// Bind the logical instance, not the physical database name: pg_restore into
	// an independent database must preserve the original instance's flow context.
	extra := d.SignInCompletionFlowContext
	// The JSON field names and order are persisted encryption context, including Extra.
	context := fingerprintJSON(struct{ SearchPath, Prefix, JWT, Extra string }{
		searchPath, d.Codes.InstancePrefix(), d.Signer.ContextFingerprint(), extra,
	})
	parts := []string{policy.Fingerprint(), d.ProviderConfiguration}
	kinds := slices.Sorted(maps.Keys(providers))
	for _, kind := range kinds {
		parts = append(parts, string(kind)+":"+providers[kind].PolicyFingerprint())
	}
	return context, fingerprintJSON(parts)
}

func (s *Service) flowAAD(id string) []byte {
	b, _ := json.Marshal([]string{"accountkit-sign-in-completion-v1", s.flowContext, id})
	return b
}

func (s *Service) encodeFlow(id string, p completionPayload) ([]byte, int16, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, 0, err
	}
	ct, v, err := s.d.Cipher.EncryptWithAAD(string(b), s.flowAAD(id))
	return ct, int16(v), err
}

func (s *Service) decodeFlow(ctx context.Context, row db.SignInCompletionFlow, dev Device) (completionPayload, error) {
	var p completionPayload
	if !row.ExpireTime.After(s.now()) {
		s.observationReason(ctx, ob.ReasonFlowExpired)
		return p, ErrSignInCompletionInvalid
	}
	if row.PolicyFingerprint != s.flowPolicy || (row.State != enum.FlowPending && row.State != enum.FlowVerified && row.State != enum.FlowCompleted) {
		return p, ErrSignInCompletionInvalid
	}
	plain, err := s.d.Cipher.DecryptWithAAD(row.Payload, uint16(row.CipherVersion), s.flowAAD(row.ID))
	if err != nil {
		return p, ErrSignInCompletionInvalid
	}
	if err := json.Unmarshal([]byte(plain), &p); err != nil {
		return p, ErrSignInCompletionInvalid
	}
	if len(p.Proofs) == 0 || (dev.ID != "" && dev.ID != p.Device.ID) || (dev.Name != "" && dev.Name != p.Device.Name) {
		return p, ErrSignInCompletionInvalid
	}
	for _, proof := range p.Proofs {
		if !s.policy.Allows(proof.Kind) {
			return p, ErrSignInCompletionInvalid
		}
	}
	return p, nil
}

func (s *Service) loadFlow(ctx context.Context, token string, dev Device) (db.SignInCompletionFlow, completionPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 {
		return db.SignInCompletionFlow{}, completionPayload{}, ErrSignInCompletionInvalid
	}
	row, err := s.d.Repo.queries().GetSignInCompletionFlowByTokenDigest(ctx, hex.EncodeToString(hashRefresh(token)))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, completionPayload{}, ErrSignInCompletionInvalid
	}
	if err != nil {
		return row, completionPayload{}, err
	}
	p, err := s.decodeFlow(ctx, row, dev)
	return row, p, err
}

func (s *Service) pendingResult(token string, expiry time.Time, hint string) SignInResult {
	return SignInResult{Pending: &PendingSignIn{SignInCompletionToken: token, ExpireTime: expiry, HintEmail: hint, EnabledIdentityKinds: s.policy.EnabledKinds(), RequiredIdentityKinds: s.policy.RequiredKinds()}}
}

func pendingHintEmail(proofs []completionProof) string {
	for _, proof := range proofs {
		if proof.Provider != nil && proof.Provider.HintEmail != "" {
			return proof.Provider.HintEmail
		}
	}
	return ""
}

func (s *Service) createPending(ctx context.Context, q *db.Queries, proof completionProof, expiry time.Time, dev Device, info *ClientInfo) (SignInResult, string, error) {
	now := s.now()
	deadline := now.Add(signInCompletionTTL)
	if !expiry.IsZero() && expiry.Before(deadline) {
		deadline = expiry
	}
	if !deadline.After(now) {
		return SignInResult{}, "", ErrSignInCompletionInvalid
	}
	token, digest, err := newRefreshToken()
	if err != nil {
		return SignInResult{}, "", err
	}
	id, err := ids.New(ids.SignInCompletionFlow)
	if err != nil {
		return SignInResult{}, "", err
	}
	p := completionPayload{Device: dev, Proofs: []completionProof{proof}}
	snapshot, err := NormalizeClientInfo(info)
	if err != nil {
		return SignInResult{}, "", err
	}
	if snapshot != nil {
		p.InitialClientInfo = snapshot
		p.InitialClientInfoObserveTime = &now
	}
	ct, version, err := s.encodeFlow(id, p)
	if err != nil {
		return SignInResult{}, "", err
	}
	_, err = q.CreateSignInCompletionFlow(ctx, db.CreateSignInCompletionFlowParams{ID: id, TokenDigest: hex.EncodeToString(digest), State: enum.FlowPending, PolicyFingerprint: s.flowPolicy, Payload: ct, CipherVersion: version, ProofUserIds: []string{}, CreateTime: now, ExpireTime: deadline})
	if err != nil {
		return SignInResult{}, "", err
	}
	return s.pendingResult(token, deadline, pendingHintEmail([]completionProof{proof})), id, nil
}

func completionRequest(token string, cred CodeCredential) string {
	b, _ := json.Marshal(cred)
	h := hmac.New(sha256.New, []byte(token))
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// CleanupSignInCompletionFlows 一次只清理固定批次，过期记录在读取时已经立即失效。
func (s *Service) CleanupSignInCompletionFlows(ctx context.Context) (total int64, resultErr error) {
	if s.observer.Enabled() {
		defer s.observeMaintenance(ctx, ob.TaskCleanupSignInCompletionFlows, func() int64 { return total }, &resultErr, time.Now())
	}
	return s.d.Repo.queries().DeleteExpiredSignInCompletionFlows(ctx, db.DeleteExpiredSignInCompletionFlowsParams{Now: s.now(), BatchSize: 1000})
}
