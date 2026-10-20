package user

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/bbxx111/accountkit/internal/userextensions"
)

// ExtensionsConfig 定义宿主授权后可写的普通 JSON 资料；nil 关闭能力。
// Schema 只作数据校验，宿主负责字段权限和隐私用途。
type ExtensionsConfig struct {
	SchemaVersion string
	Schema        json.RawMessage
}

// ExtensionsView 是独立的资料快照，不加入既有认证或 HTTP 响应。
type ExtensionsView struct {
	Data              json.RawMessage
	SchemaVersion     *string
	SchemaFingerprint *string
	Revision          int64
	UpdateTime        *time.Time
}

// ExtensionsPatch 使用 RFC 7396；ExpectedRevision 必填，0 为合法初值。
type ExtensionsPatch struct {
	Document         json.RawMessage
	ExpectedRevision *int64
}

var (
	ErrExtensionsNotConfigured  = errors.New("user: extensions not configured")
	ErrExtensionsConflict       = errors.New("user: extensions revision conflict or exhausted")
	ErrExtensionsSchemaMismatch = errors.New("user: extensions schema mismatch")
)

// ExtensionsViolation 只含已声明的 JSON pointer 与校验关键字。
type ExtensionsViolation struct{ Path, Rule string }

// ExtensionsValidationError 不保留原始值或校验器错误，最多包含 16 条原因。
type ExtensionsValidationError struct{ Violations []ExtensionsViolation }

func (*ExtensionsValidationError) Error() string        { return "user: extensions validation failed" }
func (*ExtensionsValidationError) Is(target error) bool { return target == ErrInvalidArgument }

// cloneExtensionsConfig 返回配置及其 Schema 字节的独立副本。
func cloneExtensionsConfig(c *ExtensionsConfig) *ExtensionsConfig {
	if c == nil {
		return nil
	}
	return &ExtensionsConfig{SchemaVersion: c.SchemaVersion, Schema: append(json.RawMessage(nil), c.Schema...)}
}

// ValidateExtensionsConfig 纯计算校验配置，不进行文件、网络或数据库访问。
func ValidateExtensionsConfig(c *ExtensionsConfig) error {
	if c == nil {
		return nil
	}
	_, err := userextensions.Compile(c.SchemaVersion, c.Schema)
	if err != nil {
		return ErrInvalidArgument
	}
	return nil
}
func extensionsValidation(err error) error {
	var e *userextensions.ValidationError
	if !errors.As(err, &e) {
		return ErrInvalidArgument
	}
	result := &ExtensionsValidationError{}
	for _, v := range e.Violations {
		if len(result.Violations) == 16 {
			break
		}
		result.Violations = append(result.Violations, ExtensionsViolation{Path: v.Path, Rule: v.Rule})
	}
	return result
}
