package user

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/profileattributes"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

var errProfileAttributesInvariant = errors.New("user: profile_attributes storage invariant violated")
var errProfileAttributesStorage = errors.New("user: profile_attributes storage operation failed; commit outcome may require read-back")
var profileAttributesVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var profileAttributesFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func profileAttributesView(raw []byte, version, fingerprint *string, updated *time.Time) (ProfileAttributesView, error) {
	data, err := profileattributes.NormalizeStored(raw)
	if err != nil || (version == nil) != (fingerprint == nil) || (version != nil && (!profileAttributesVersionPattern.MatchString(*version) || !profileAttributesFingerprintPattern.MatchString(*fingerprint))) {
		return ProfileAttributesView{}, errProfileAttributesInvariant
	}
	if version == nil && (string(data) != "{}" || updated != nil) || version != nil && updated == nil {
		return ProfileAttributesView{}, errProfileAttributesInvariant
	}
	v := ProfileAttributesView{Data: data}
	if version != nil {
		x := *version
		v.SchemaVersion = &x
		x2 := *fingerprint
		v.SchemaFingerprint = &x2
	}
	if updated != nil {
		x := *updated
		v.UpdateTime = &x
	}
	return v, nil
}

// GetProfileAttributes 读取 ACTIVE/FROZEN 账号，不使用当前 Schema 重验历史资料。
func (s *Service) GetProfileAttributes(ctx context.Context, userID string) (ProfileAttributesView, error) {
	if s.profileAttributes == nil {
		return ProfileAttributesView{}, ErrProfileAttributesNotConfigured
	}
	if !ids.Valid(ids.User, userID) {
		return ProfileAttributesView{}, ErrInvalidArgument
	}
	row, err := s.d.Repo.Q().GetProfileAttributes(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileAttributesView{}, ErrNotFound
	}
	if err != nil {
		return ProfileAttributesView{}, safeProfileAttributesError(err)
	}
	if row.State != enum.UserActive && row.State != enum.UserFrozen {
		return ProfileAttributesView{}, ErrInvalidState
	}
	return profileAttributesView(row.ProfileAttributes, row.ProfileAttributesSchemaVersion, row.ProfileAttributesSchemaFingerprint, row.ProfileAttributesUpdateTime)
}

// PatchProfileAttributes 在账号锁内校验最新状态，提交成功后才返回和投递审计。
// 提交错误可能有不确定结果，宿主应重新读取核对，不能盲目重放。
func (s *Service) PatchProfileAttributes(ctx context.Context, userID string, patch ProfileAttributesPatch, meta Meta) (ProfileAttributesView, error) {
	if s.profileAttributes == nil {
		return ProfileAttributesView{}, ErrProfileAttributesNotConfigured
	}
	if !ids.Valid(ids.User, userID) {
		return ProfileAttributesView{}, ErrInvalidArgument
	}
	decoded, err := profileattributes.DecodePatch(patch.Document)
	if err != nil {
		return ProfileAttributesView{}, ErrInvalidArgument
	}
	var view ProfileAttributesView
	changed := false
	err = s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		u, err := q.LockUserByID(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if u.State != enum.UserActive {
			return ErrInvalidState
		}

		view, err = profileAttributesView(u.ProfileAttributes, u.ProfileAttributesSchemaVersion, u.ProfileAttributesSchemaFingerprint, u.ProfileAttributesUpdateTime)
		if err != nil {
			return err
		}
		if view.SchemaVersion != nil && (*view.SchemaVersion != s.profileAttributes.Version() || *view.SchemaFingerprint != s.profileAttributes.Fingerprint()) {
			return ErrProfileAttributesSchemaMismatch
		}
		data, actual, err := s.profileAttributes.Apply(view.Data, decoded)
		if err != nil {
			return profileAttributesValidation(err)
		}
		if !actual {
			return nil
		}

		row, err := q.UpdateProfileAttributes(ctx, db.UpdateProfileAttributesParams{ID: userID, ProfileAttributes: data, ProfileAttributesSchemaVersion: s.profileAttributes.Version(), ProfileAttributesSchemaFingerprint: s.profileAttributes.Fingerprint(), ActiveState: enum.UserActive, Now: sessionTime(s.now())})
		if errors.Is(err, pgx.ErrNoRows) {
			return errProfileAttributesInvariant
		}
		if err != nil {
			return err
		}
		view, err = profileAttributesView(row.ProfileAttributes, row.ProfileAttributesSchemaVersion, row.ProfileAttributesSchemaFingerprint, row.ProfileAttributesUpdateTime)
		changed = err == nil
		return err
	})
	if err != nil {
		return ProfileAttributesView{}, safeProfileAttributesError(err)
	}
	if changed {
		s.recordProfileAttributesUpdated(ctx, userID, view, meta.RequestID)
	}
	return view, nil
}

// Never retain a PgError: its Detail can include the entire private new row.
func safeProfileAttributesError(err error) error {
	for _, known := range []error{ErrNotFound, ErrInvalidArgument, ErrInvalidState, ErrProfileAttributesSchemaMismatch, errProfileAttributesInvariant, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return err
		}
	}
	return errProfileAttributesStorage
}
