package idp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidSignedRequest：Facebook signed_request 格式、算法或签名不符。
var ErrInvalidSignedRequest = errors.New("idp: invalid signed request")

// FacebookDeletionRequest 是校验通过的数据删除回调内容。
type FacebookDeletionRequest struct {
	UserID    string
	IssueTime time.Time
}

// ParseSignedRequest 校验 Meta 数据删除回调的 signed_request：<base64url(sig)>.<base64url(payload)>，
// 无填充；先以 App Secret 对 payload 段做 HMAC-SHA256 常量时间比较，再解析 payload。
func (f *Facebook) ParseSignedRequest(raw string) (FacebookDeletionRequest, error) {
	sigPart, payloadPart, ok := strings.Cut(raw, ".")
	if !ok || sigPart == "" || payloadPart == "" || strings.Contains(payloadPart, ".") {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: malformed", ErrInvalidSignedRequest)
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: signature encoding", ErrInvalidSignedRequest)
	}
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(payloadPart))
	if f.secret == "" || !hmac.Equal(sig, mac.Sum(nil)) {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: signature mismatch", ErrInvalidSignedRequest)
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload encoding", ErrInvalidSignedRequest)
	}
	var p struct {
		Algorithm string `json:"algorithm"`
		UserID    string `json:"user_id"`
		IssuedAt  int64  `json:"issued_at"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload json", ErrInvalidSignedRequest)
	}
	if !strings.EqualFold(p.Algorithm, "HMAC-SHA256") || p.UserID == "" || p.IssuedAt <= 0 {
		return FacebookDeletionRequest{}, fmt.Errorf("%w: payload fields", ErrInvalidSignedRequest)
	}
	return FacebookDeletionRequest{UserID: p.UserID, IssueTime: time.Unix(p.IssuedAt, 0)}, nil
}
