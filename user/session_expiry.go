package user

import (
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/user/db"
)

// errSessionExpired 保留公开错误分类，同时让重新认证审计区分自然到期与明确吊销。
var errSessionExpired = fmt.Errorf("%w: session expired", ErrInvalidToken)

// sessionTime 对齐 PostgreSQL timestamptz 的微秒精度，统一期限判断的时区和边界。
func sessionTime(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

// sessionActiveAt 只用于可续用判断；明确撤销仍应涵盖尚未清理的到期会话。
func sessionActiveAt(sess db.Session, at time.Time) bool {
	return sess.RevokeTime == nil && sess.RefreshExpireTime.After(sessionTime(at))
}
