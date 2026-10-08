package user

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestWithActiveUsersValidatesBeforeIO(t *testing.T) {
	// 未装配的 Service 只能用于这个边界测试：参数拒绝必须先于借连接。
	s := &Service{}
	for _, tc := range []struct {
		name string
		ids  []string
		fn   func(pgx.Tx) error
	}{
		{"empty", nil, func(pgx.Tx) error { return nil }},
		{"nil_callback", []string{"u_0000000000001"}, nil},
		{"invalid", []string{"u_0000000000001", "bad-id"}, func(pgx.Tx) error { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.WithActiveUsers(context.Background(), tc.ids, tc.fn); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid arguments: %v", err)
			}
		})
	}
}
