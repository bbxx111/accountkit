package user

import (
	"encoding/json"
	"errors"

	"github.com/bbxx111/accountkit/internal/profileattributes"
)

// ProfileAttributesConfig 定义用户 JSON 资料；nil 关闭能力。
// 内置 HTTP 允许本人完整读写；Go 直接调用由宿主授权。Schema 只作数据校验，
// readOnly/writeOnly 不限制本人访问；业务权益、权限和凭据保持原领域归属。
type ProfileAttributesConfig struct {
	Schema json.RawMessage
}

// ProfileAttributesView 是独立的资料快照，不加入既有认证或 HTTP 响应。
type ProfileAttributesView struct {
	Data json.RawMessage
}

// ProfileAttributesPatch 使用 RFC 7396；补丁在数据库账号行锁内合并最新资料。
type ProfileAttributesPatch struct {
	Document json.RawMessage
}

var (
	ErrProfileAttributesNotConfigured = errors.New("user: profile_attributes not configured")
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
	return &ProfileAttributesConfig{Schema: append(json.RawMessage(nil), c.Schema...)}
}

// ValidateProfileAttributesConfig 纯计算校验配置，不进行文件、网络或数据库访问。
func ValidateProfileAttributesConfig(c *ProfileAttributesConfig) error {
	if c == nil {
		return nil
	}
	_, err := profileattributes.Compile(c.Schema)
	if err != nil {
		return ErrInvalidArgument
	}
	return nil
}
func profileAttributesValidation(err error) error {
	var e *profileattributes.ValidationError
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
