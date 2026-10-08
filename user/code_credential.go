package user

import (
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"time"
)

// CodeChallenge 是发送器接受投递后返回的验证码轮次。
type CodeChallenge struct {
	CodeID     string
	ExpireTime time.Time
}

// CodeCredential 是 PHONE/EMAIL 操作必需的完整轮次凭证。
type CodeCredential struct {
	Channel enum.IdentityKind
	Target  string
	CodeID  string
	Code    string
}

func validateCodeCredential(c CodeCredential) error {
	if len(c.CodeID) != 32 {
		return fmt.Errorf("%w: code_id must be 32 lowercase hexadecimal characters", ErrInvalidArgument)
	}
	for _, ch := range c.CodeID {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return fmt.Errorf("%w: invalid code_id", ErrInvalidArgument)
		}
	}
	if c.Target == "" || c.Code == "" {
		return fmt.Errorf("%w: target and code are required", ErrInvalidArgument)
	}
	return nil
}
