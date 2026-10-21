package user

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/bbxx111/accountkit/internal/userextensions"
)

// ProfileAttributesConfig 定义宿主授权后可写的用户 JSON 资料；nil 关闭能力。
// Schema 只作数据校验，宿主负责字段权限和隐私用途；业务权益、权限和凭据保持原领域归属。
type ProfileAttributesConfig struct {
	SchemaVersion string
	Schema        json.RawMessage
}

// ProfileAttributesView 是独立的资料快照，不加入既有认证或 HTTP 响应。
type ProfileAttributesView struct {
	Data              json.RawMessage
	SchemaVersion     *string
	SchemaFingerprint *string
	UpdateTime        *time.Time
}

// ProfileAttributesPatch 使用 RFC 7396；补丁在数据库账号行锁内合并最新资料。
type ProfileAttributesPatch struct {
	Document json.RawMessage
}

var (
	ErrProfileAttributesNotConfigured  = errors.New("user: profile_attributes not configured")
	ErrProfileAttributesSchemaMismatch = errors.New("user: profile_attributes schema mismatch")
)

// ProfileAttributesViolation 只含已声明的 JSON pointer 与校验关键字。
type ProfileAttributesViolation struct{ Path, Rule string }

// ProfileAttributesValidationError 不保留原始值或校验器错误，最多包含 16 条原因。
type ProfileAttributesValidationError struct{ Violations []ProfileAttributesViolation }

func (*ProfileAttributesValidationError) Error() string {
	return "user: profile_attributes validation failed"
}
func (*ProfileAttributesValidationError) Is(target error) bool { return target == ErrInvalidArgument }

// cloneProfileAttributesConfig 返回配置及其 Schema 字节的独立副本。
func cloneProfileAttributesConfig(c *ProfileAttributesConfig) *ProfileAttributesConfig {
	if c == nil {
		return nil
	}
	return &ProfileAttributesConfig{SchemaVersion: c.SchemaVersion, Schema: append(json.RawMessage(nil), c.Schema...)}
}

// ValidateProfileAttributesConfig 纯计算校验配置，不进行文件、网络或数据库访问。
func ValidateProfileAttributesConfig(c *ProfileAttributesConfig) error {
	if c == nil {
		return nil
	}
	_, err := userextensions.Compile(c.SchemaVersion, c.Schema)
	if err != nil {
		return ErrInvalidArgument
	}
	return nil
}
func profileAttributesValidation(err error) error {
	var e *userextensions.ValidationError
	if !errors.As(err, &e) {
		return ErrInvalidArgument
	}
	result := &ProfileAttributesValidationError{}
	for _, v := range e.Violations {
		if len(result.Violations) == 16 {
			break
		}
		result.Violations = append(result.Violations, ProfileAttributesViolation{Path: v.Path, Rule: v.Rule})
	}
	return result
}
