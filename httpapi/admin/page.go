package admin

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

// parsePageSize：缺省或 0 → 默认 20；> 100 按 100（AIP-158：超过上限按上限处理）；非整数或负数 → 400。
func parsePageSize(raw string) (int, *apierror.Error) {
	if raw == "" {
		return user.AdminPageSizeDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, apierror.New(apierror.StatusInvalidArgument, "INVALID_PAGE_SIZE", "page_size must be a non-negative integer")
	}
	if n == 0 {
		return user.AdminPageSizeDefault, nil
	}
	if n > user.AdminPageSizeMax {
		return user.AdminPageSizeMax, nil
	}
	return n, nil
}

// cursorToken 是 page_token 的明文形状；对客户端不透明（base64url，无填充）。
type cursorToken struct {
	T  time.Time `json:"t"`
	ID string    `json:"id"`
}

func encodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(cursorToken{T: t.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor：空串 → ok=false（从头开始）；无法解析或字段缺失 → 400 INVALID_PAGE_TOKEN。
func decodeCursor(raw string) (time.Time, string, bool, *apierror.Error) {
	if raw == "" {
		return time.Time{}, "", false, nil
	}
	bad := apierror.New(apierror.StatusInvalidArgument, "INVALID_PAGE_TOKEN", "page_token is invalid or expired; restart from the first page")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", false, bad
	}
	var c cursorToken
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" || c.T.IsZero() {
		return time.Time{}, "", false, bad
	}
	return c.T, c.ID, true, nil
}
