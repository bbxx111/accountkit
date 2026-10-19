package user

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
)

// ErrInvalidClientInfo 表示显式客户端描述不符合有界文本和枚举契约。
var ErrInvalidClientInfo = errors.New("user: invalid client info")

// ClientInfo 是可选、自报的结构化客户端描述，不是凭证或可信设备证明。
// 空字符串与枚举零值表示未观察到该字段；不包含原始 UA。
type ClientInfo struct {
	ClientType     enum.ClientType
	OSName         string
	OSVersion      string
	BrowserName    string
	BrowserVersion string
	AppName        string
	AppVersion     string
	DeviceType     enum.DeviceType
	DeviceModel    string
}

// NormalizeClientInfo 校验并复制观察输入。空描述返回 nil；不会修改输入。
// 版本单独出现时保留为观察值，由 MergeClientInfo 结合已知名称合并。
func NormalizeClientInfo(input *ClientInfo) (*ClientInfo, error) {
	if input == nil {
		return nil, nil
	}
	out := *input
	if out.ClientType != 0 && !out.ClientType.Valid() || out.DeviceType != 0 && !out.DeviceType.Valid() {
		return nil, ErrInvalidClientInfo
	}
	total := 0
	for _, field := range []struct {
		value *string
		limit int
	}{
		{&out.OSName, 64}, {&out.OSVersion, 32}, {&out.BrowserName, 64}, {&out.BrowserVersion, 32}, {&out.AppName, 64}, {&out.AppVersion, 32}, {&out.DeviceModel, 64},
	} {
		// 先拒绝控制字符，再 trim，不能通过边界空白隐去非法输入。
		if !utf8.ValidString(*field.value) {
			return nil, ErrInvalidClientInfo
		}
		for _, r := range *field.value {
			if unicode.IsControl(r) {
				return nil, ErrInvalidClientInfo
			}
		}
		*field.value = strings.TrimSpace(*field.value)
		if utf8.RuneCountInString(*field.value) > field.limit {
			return nil, ErrInvalidClientInfo
		}
		total += len(*field.value)
	}
	if total > 2048 {
		return nil, ErrInvalidClientInfo
	}
	clearInapplicableClientInfo(&out)
	if out == (ClientInfo{}) {
		return nil, nil
	}
	return &out, nil
}

// MergeClientInfo 合并已规范化的观察值，不修改或复用输入指针。
// 缺失字段保留；名称变化但缺版本时清除旧版本；无已知名称的版本不进入快照。
func MergeClientInfo(previous, observed *ClientInfo) *ClientInfo {
	out := ClientInfo{}
	if previous != nil {
		out = *previous
	}
	if observed != nil {
		if observed.ClientType != 0 {
			out.ClientType = observed.ClientType
		}
		mergeClientSoftware(&out.OSName, &out.OSVersion, observed.OSName, observed.OSVersion)
		mergeClientSoftware(&out.BrowserName, &out.BrowserVersion, observed.BrowserName, observed.BrowserVersion)
		mergeClientSoftware(&out.AppName, &out.AppVersion, observed.AppName, observed.AppVersion)
		if observed.DeviceType != 0 {
			out.DeviceType = observed.DeviceType
		}
		if observed.DeviceModel != "" {
			out.DeviceModel = observed.DeviceModel
		}
	}
	clearInapplicableClientInfo(&out)
	if out.OSName == "" {
		out.OSVersion = ""
	}
	if out.BrowserName == "" {
		out.BrowserVersion = ""
	}
	if out.AppName == "" {
		out.AppVersion = ""
	}
	if out == (ClientInfo{}) {
		return nil
	}
	return &out
}

func mergeClientSoftware(name, version *string, observedName, observedVersion string) {
	if observedName != "" {
		if observedName != *name {
			*version = ""
		}
		*name = observedName
	}
	if observedVersion != "" && *name != "" {
		*version = observedVersion
	}
}

func clearInapplicableClientInfo(out *ClientInfo) {
	switch out.ClientType {
	case enum.ClientWeb:
		out.AppName = ""
		out.AppVersion = ""
	case enum.ClientNative:
		out.BrowserName = ""
		out.BrowserVersion = ""
	}
}
