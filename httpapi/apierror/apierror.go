// Package apierror 写出 AIP-193 错误体与 RFC 6749 §5.2 错误体。
//
// 只做序列化与状态码映射；不做任何领域判断。500 的 message 固定为 "internal error"，
// 真实错误由 WriteInternal 写入日志（带 request_id）。
package apierror

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// google.rpc.Code 名称（AIP-193）。
const (
	StatusInvalidArgument    = "INVALID_ARGUMENT"
	StatusFailedPrecondition = "FAILED_PRECONDITION"
	StatusUnauthenticated    = "UNAUTHENTICATED"
	StatusPermissionDenied   = "PERMISSION_DENIED"
	StatusNotFound           = "NOT_FOUND"
	StatusResourceExhausted  = "RESOURCE_EXHAUSTED"
	StatusInternal           = "INTERNAL"
	StatusUnavailable        = "UNAVAILABLE"
	StatusAlreadyExists      = "ALREADY_EXISTS"
)

var httpStatus = map[string]int{
	StatusInvalidArgument:    http.StatusBadRequest,
	StatusFailedPrecondition: http.StatusBadRequest, // AIP-193 规范映射：400，而非 409
	StatusUnauthenticated:    http.StatusUnauthorized,
	StatusPermissionDenied:   http.StatusForbidden,
	StatusNotFound:           http.StatusNotFound,
	StatusResourceExhausted:  http.StatusTooManyRequests,
	StatusInternal:           http.StatusInternalServerError,
	StatusUnavailable:        http.StatusServiceUnavailable,
	StatusAlreadyExists:      http.StatusConflict,
}

// HTTPStatus 返回 status 对应的 HTTP 状态码；未知 status 视为 500。
func HTTPStatus(status string) int {
	if c, ok := httpStatus[status]; ok {
		return c
	}
	return http.StatusInternalServerError
}

// Error 是一条可写出的 AIP-193 错误。
type Error struct {
	Status            string
	Reason            string
	Message           string
	RetryAfterSeconds int
}

func (e *Error) Error() string {
	if e.Reason != "" {
		return e.Status + "/" + e.Reason + ": " + e.Message
	}
	return e.Status + ": " + e.Message
}

// New 构造 Error。
func New(status, reason, message string) *Error {
	return &Error{Status: status, Reason: reason, Message: message}
}

type body struct {
	Error bodyError `json:"error"`
}

type bodyError struct {
	Code              int    `json:"code"`
	Message           string `json:"message"`
	Status            string `json:"status"`
	Reason            string `json:"reason,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

// Write 写出 AIP-193 错误体；RetryAfterSeconds > 0 时同时设置 Retry-After 头。
func Write(w http.ResponseWriter, e *Error) {
	code := HTTPStatus(e.Status)
	if e.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfterSeconds))
	}
	WriteJSON(w, code, body{Error: bodyError{
		Code: code, Message: e.Message, Status: e.Status, Reason: e.Reason, RetryAfterSeconds: e.RetryAfterSeconds,
	}})
}

// WriteInternal 记录真实错误并写出固定文案的 500。
func WriteInternal(w http.ResponseWriter, logger *slog.Logger, requestID string, err error) {
	if logger != nil {
		logger.Error("internal error", "request_id", requestID, "err", err)
	}
	Write(w, &Error{Status: StatusInternal, Message: "internal error"})
}

// WriteOAuth 写出 RFC 6749 §5.2 形状的错误体（/token、/revoke 专用），extra 中的键并列写入。
func WriteOAuth(w http.ResponseWriter, httpStatus int, code, description string, extra map[string]any) {
	m := map[string]any{"error": code}
	if description != "" {
		m["error_description"] = description
	}
	for k, v := range extra {
		m[k] = v
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	WriteJSON(w, httpStatus, m)
}

// WriteJSON 以 application/json 写出 v。
func WriteJSON(w http.ResponseWriter, httpStatus int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteRouteNotFound 写路由不存在的 404（与 WriteJSON 同形的 AIP-193 体）。
func WriteRouteNotFound(w http.ResponseWriter) {
	Write(w, New(StatusNotFound, "ROUTE_NOT_FOUND", "no such route"))
}

// WriteMethodNotAllowed 写 405：AIP-193 没有对应的 google.rpc.Code，body 沿用其形状（status INVALID_ARGUMENT，
// reason METHOD_NOT_ALLOWED）但真实 HTTP 状态码是 405；不带 Allow 头（3b 决策）。
func WriteMethodNotAllowed(w http.ResponseWriter) {
	WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{
		"error": map[string]any{
			"code":    http.StatusMethodNotAllowed,
			"message": "method not allowed for this route",
			"status":  StatusInvalidArgument,
			"reason":  "METHOD_NOT_ALLOWED",
		},
	})
}
