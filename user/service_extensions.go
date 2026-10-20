package user

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/userextensions"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

var errExtensionsInvariant = errors.New("user: extensions storage invariant violated")
var errExtensionsStorage = errors.New("user: extensions storage operation failed; commit outcome may require read-back")
var extensionsVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
var extensionsFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func extensionsView(raw []byte, version, fingerprint *string, revision int64, updated *time.Time) (ExtensionsView, error) {
	data, err := userextensions.NormalizeStored(raw)
	if err != nil || revision < 0 || (version == nil) != (fingerprint == nil) || (version != nil && (!extensionsVersionPattern.MatchString(*version) || !extensionsFingerprintPattern.MatchString(*fingerprint))) {
		return ExtensionsView{}, errExtensionsInvariant
	}
	if revision == 0 && (version != nil || updated != nil || string(data) != "{}") {
		return ExtensionsView{}, errExtensionsInvariant
	}
	if revision > 0 && (version == nil || updated == nil) {
		return ExtensionsView{}, errExtensionsInvariant
	}
	v := ExtensionsView{Data: data, Revision: revision}
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

// GetExtensions 读取 ACTIVE/FROZEN 账号，不使用当前 Schema 重验历史资料。
func (s *Service) GetExtensions(ctx context.Context, userID string) (ExtensionsView, error) {
	if s.extensions == nil {
		return ExtensionsView{}, ErrExtensionsNotConfigured
	}
	if !ids.Valid(ids.User, userID) {
		return ExtensionsView{}, ErrInvalidArgument
	}
	row, err := s.d.Repo.Q().GetUserExtensions(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExtensionsView{}, ErrNotFound
	}
	if err != nil {
		return ExtensionsView{}, safeExtensionsError(err)
	}
	if row.State != enum.UserActive && row.State != enum.UserFrozen {
		return ExtensionsView{}, ErrInvalidState
	}
	return extensionsView(row.Extensions, row.ExtensionsSchemaVersion, row.ExtensionsSchemaFingerprint, row.ExtensionsRevision, row.ExtensionsUpdateTime)
}

// PatchExtensions 在账号锁内校验最新状态和 revision，提交成功后才返回和投递审计。
// 提交错误可能有不确定结果，宿主应重新读取核对，不能盲目重放。
func (s *Service) PatchExtensions(ctx context.Context, userID string, patch ExtensionsPatch, meta Meta) (ExtensionsView, error) {
	if s.extensions == nil {
		return ExtensionsView{}, ErrExtensionsNotConfigured
	}
	if !ids.Valid(ids.User, userID) || patch.ExpectedRevision == nil || *patch.ExpectedRevision < 0 {
		return ExtensionsView{}, ErrInvalidArgument
	}
	expected := *patch.ExpectedRevision
	decoded, err := userextensions.DecodePatch(patch.Document)
	if err != nil {
		return ExtensionsView{}, ErrInvalidArgument
	}
	var view ExtensionsView
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
		if u.ExtensionsRevision != expected {
			return ErrExtensionsConflict
		}
		view, err = extensionsView(u.Extensions, u.ExtensionsSchemaVersion, u.ExtensionsSchemaFingerprint, u.ExtensionsRevision, u.ExtensionsUpdateTime)
		if err != nil {
			return err
		}
		if view.SchemaVersion != nil && (*view.SchemaVersion != s.extensions.Version() || *view.SchemaFingerprint != s.extensions.Fingerprint()) {
			return ErrExtensionsSchemaMismatch
		}
		data, actual, err := s.extensions.Apply(view.Data, decoded)
		if err != nil {
			return extensionsValidation(err)
		}
		if !actual {
			return nil
		}
		if view.Revision == math.MaxInt64 {
			return ErrExtensionsConflict
		}
		row, err := q.UpdateUserExtensions(ctx, db.UpdateUserExtensionsParams{ID: userID, Extensions: data, ExtensionsSchemaVersion: s.extensions.Version(), ExtensionsSchemaFingerprint: s.extensions.Fingerprint(), ExpectedRevision: expected, ActiveState: enum.UserActive, Now: sessionTime(s.now())})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrExtensionsConflict
		}
		if err != nil {
			return err
		}
		view, err = extensionsView(row.Extensions, row.ExtensionsSchemaVersion, row.ExtensionsSchemaFingerprint, row.ExtensionsRevision, row.ExtensionsUpdateTime)
		changed = err == nil
		return err
	})
	if err != nil {
		return ExtensionsView{}, safeExtensionsError(err)
	}
	if changed {
		s.recordExtensionsUpdated(ctx, userID, view, meta.RequestID)
	}
	return view, nil
}

// Never retain a PgError: its Detail can include the entire private new row.
func safeExtensionsError(err error) error {
	for _, known := range []error{ErrNotFound, ErrInvalidArgument, ErrInvalidState, ErrExtensionsConflict, ErrExtensionsSchemaMismatch, errExtensionsInvariant, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return err
		}
	}
	return errExtensionsStorage
}
