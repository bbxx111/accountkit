package user

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/jackc/pgx/v5"
)

// ProfilePatch 选择要更新的基础资料；nil 保持原值，空字符串清空。
// 所有提供的字段均在写入前校验。
type ProfilePatch struct {
	DisplayName *string
	AvatarURL   *string
}

func normalizeAvatarURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || len(value) > 2048 {
		return "", fmt.Errorf("%w: invalid avatar_url", ErrInvalidArgument)
	}
	if value == "" {
		return "", nil
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' || r == '#' {
			return "", fmt.Errorf("%w: invalid avatar_url", ErrInvalidArgument)
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return "", fmt.Errorf("%w: invalid avatar_url", ErrInvalidArgument)
	}
	return value, nil
}

// UpdateProfile 在账号行锁内复核最新 ACTIVE 状态，原子修改所选基础资料。
// 头像只校验地址，不访问远程资源。
func (s *Service) UpdateProfile(ctx context.Context, userID string, patch ProfilePatch) (Me, error) {
	if !ids.Valid(ids.User, userID) || (patch.DisplayName == nil && patch.AvatarURL == nil) {
		return Me{}, ErrInvalidArgument
	}
	var name, avatar *string
	if patch.DisplayName != nil {
		value, err := normalizeDisplayName(*patch.DisplayName)
		if err != nil {
			return Me{}, err
		}
		if value != "" {
			name = &value
		}
	}
	if patch.AvatarURL != nil {
		value, err := normalizeAvatarURL(*patch.AvatarURL)
		if err != nil {
			return Me{}, err
		}
		if value != "" {
			avatar = &value
		}
	}
	var result Me
	err := s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		row, err := q.LockUserByID(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("user: lock profile: %w", err)
		}
		if row.State != enum.UserActive {
			return ErrInvalidState
		}
		row, err = q.UpdateUserProfile(ctx, db.UpdateUserProfileParams{ID: userID, SetDisplayName: patch.DisplayName != nil, DisplayName: name, SetAvatarUrl: patch.AvatarURL != nil, AvatarUrl: avatar, ActiveState: enum.UserActive, Now: sessionTime(s.now())})
		if err != nil {
			return fmt.Errorf("user: update profile: %w", err)
		}
		result = meFrom(row)
		return nil
	})
	if err != nil {
		return Me{}, err
	}
	return result, nil
}
