// Package jsonbody 是 C 端与管理面共用的严格 JSON 请求体解码：单个 JSON 对象、拒绝未知字段、大小上限、
// 拒绝多余内容。任何问题都归为 ErrMalformed（细节由调用方按需记 Debug 日志）。
package jsonbody

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/bbxx111/accountkit/httpapi/apierror"
)

// ErrMalformed：body 缺失、非法 JSON、顶层不是对象、未知字段、超长或有多余内容。
var ErrMalformed = errors.New("jsonbody: malformed body")

// DefaultMaxBytes 是请求体上限（64 KiB）。
const DefaultMaxBytes = 64 << 10

// Decode 以 maxBytes 上限、严格模式把 body 解码进 dst。body 必须是单个 JSON 对象：先取原始值判断顶层 token
// 是 '{'（拒绝 JSON null / 数组 / 字符串等合法但不是对象的顶层值），再第二遍以 DisallowUnknownFields 解码。
func Decode(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: trailing content", ErrMalformed)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: top-level value is not an object", ErrMalformed)
	}
	valueDec := json.NewDecoder(bytes.NewReader(raw))
	valueDec.DisallowUnknownFields()
	if err := valueDec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return nil
}

// Malformed 返回统一的 400 MALFORMED_BODY。
func Malformed() *apierror.Error {
	return apierror.New(apierror.StatusInvalidArgument, "MALFORMED_BODY", "request body must be a single JSON object with known fields (<= 64 KiB)")
}
