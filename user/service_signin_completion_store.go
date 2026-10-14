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
	"net"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
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
	Device           Device
	Proofs           []completionProof
	Requests         []string
	FinalRequest     string
	Result           *TokenResult
	AccessExpireTime time.Time
}

func fingerprintJSON(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func identityFlowConfiguration(d Deps, policy *IdentityPolicy) (string, string) {
	var searchPath string
	if d.Repo.pool != nil {
		cfg := d.Repo.pool.Config().ConnConfig
		first, _, _ := strings.Cut(cfg.RuntimeParams["search_path"], ",")
		searchPath = strings.Trim(strings.TrimSpace(first), `"`)
	}
	// Bind the logical instance, not the physical database name: pg_restore into
	// an independent database must preserve the original instance's flow context.
	context := fingerprintJSON(struct{ SearchPath, Prefix, JWT, Extra string }{
		searchPath, d.Codes.InstancePrefix(), d.Signer.ContextFingerprint(), d.IdentityFlowContext,
	})
	provider := func(v any) string {
		if p, ok := v.(interface{ PolicyFingerprint() string }); ok {
			return p.PolicyFingerprint()
		}
		return fmt.Sprintf("%T", v)
	}
	return context, fingerprintJSON([]string{policy.Fingerprint(), provider(d.WeChat), provider(d.Apple), d.ProviderConfiguration})
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

func (s *Service) decodeFlow(row db.IdentityFlow, dev Device) (completionPayload, error) {
	var p completionPayload
	if !row.ExpireTime.After(s.now()) || row.PolicyFingerprint != s.flowPolicy || (row.State != enum.FlowPending && row.State != enum.FlowVerified && row.State != enum.FlowCompleted) {
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

func (s *Service) loadFlow(ctx context.Context, token string, dev Device) (db.IdentityFlow, completionPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 32 {
		return db.IdentityFlow{}, completionPayload{}, ErrSignInCompletionInvalid
	}
	row, err := s.d.Repo.Q().GetIdentityFlowByTokenDigest(ctx, hex.EncodeToString(hashRefresh(token)))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, completionPayload{}, ErrSignInCompletionInvalid
	}
	if err != nil {
		return row, completionPayload{}, err
	}
	p, err := s.decodeFlow(row, dev)
	return row, p, err
}

func (s *Service) pendingResult(token string, expiry time.Time) SignInResult {
	return SignInResult{Pending: &PendingSignIn{SignInCompletionToken: token, ExpireTime: expiry, EnabledIdentityKinds: s.policy.EnabledKinds(), RequiredIdentityKinds: s.policy.RequiredKinds()}}
}

func (s *Service) createPending(ctx context.Context, q *db.Queries, proof completionProof, expiry time.Time, dev Device) (SignInResult, error) {
	now := s.now()
	deadline := now.Add(signInCompletionTTL)
	if !expiry.IsZero() && expiry.Before(deadline) {
		deadline = expiry
	}
	if !deadline.After(now) {
		return SignInResult{}, ErrSignInCompletionInvalid
	}
	token, digest, err := newRefreshToken()
	if err != nil {
		return SignInResult{}, err
	}
	id, err := ids.New(ids.IdentityFlow)
	if err != nil {
		return SignInResult{}, err
	}
	p := completionPayload{Device: dev, Proofs: []completionProof{proof}}
	ct, version, err := s.encodeFlow(id, p)
	if err != nil {
		return SignInResult{}, err
	}
	_, err = q.CreateIdentityFlow(ctx, db.CreateIdentityFlowParams{ID: id, TokenDigest: hex.EncodeToString(digest), State: enum.FlowPending, PolicyFingerprint: s.flowPolicy, Payload: ct, CipherVersion: version, ProofUserIds: []string{}, CreateTime: now, ExpireTime: deadline})
	if err != nil {
		return SignInResult{}, err
	}
	return s.pendingResult(token, deadline), nil
}

func completionRequest(token string, cred CodeCredential) string {
	b, _ := json.Marshal(cred)
	h := hmac.New(sha256.New, []byte(token))
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// CleanupIdentityFlows 一次只清理固定批次，过期记录在读取时已经立即失效。
func (s *Service) CleanupIdentityFlows(ctx context.Context) (int64, error) {
	return s.d.Repo.Q().DeleteExpiredIdentityFlows(ctx, db.DeleteExpiredIdentityFlowsParams{Now: s.now(), BatchSize: 1000})
}
