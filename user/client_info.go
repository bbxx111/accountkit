package user

import (
	"encoding/json"
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
	ClientType         enum.ClientType
	OSName             string
	OSVersion          string
	BrowserName        string
	BrowserVersion     string
	AppName            string
	AppVersion         string
	DeviceType         enum.DeviceType
	DeviceModel        string
	DeviceManufacturer string
	AppBuild           string
	Extensions         map[string]json.RawMessage
}

// NormalizeClientInfo 校验并复制观察输入。空描述返回 nil；不会修改输入。
// 版本/build 单独出现时也是合法观察值；不从历史描述补名称。
func NormalizeClientInfo(input *ClientInfo) (*ClientInfo, error) {
	if input == nil {
		return nil, nil
	}
	out := *input
	if out.ClientType != 0 && !out.ClientType.Valid() || out.DeviceType != 0 && !out.DeviceType.Valid() {
		return nil, ErrInvalidClientInfo
	}
	for _, field := range []struct {
		value *string
		limit int
	}{
		{&out.OSName, 64}, {&out.OSVersion, 32}, {&out.BrowserName, 64}, {&out.BrowserVersion, 32}, {&out.AppName, 64}, {&out.AppVersion, 32}, {&out.DeviceModel, 64},
		{&out.DeviceManufacturer, 64}, {&out.AppBuild, 64},
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
	}
	var err error
	out.Extensions, err = normalizeClientExtensions(input.Extensions)
	if err != nil {
		return nil, err
	}
	if clientInfoEmpty(&out) {
		return nil, nil
	}
	encoded, err := json.Marshal(clientInfoJSONFrom(&out))
	if err != nil || len(encoded) > 2048 {
		return nil, ErrInvalidClientInfo
	}
	return &out, nil
}

// MergeClientInfo 仅供同一次 HTTP 请求中的 UA、Hints、显式值按优先级覆盖。
// 缺失字段保留；名称变化但缺版本时清除低优先级版本，不用于跨请求持久化合并。
func MergeClientInfo(previous, observed *ClientInfo) *ClientInfo {
	out := ClientInfo{}
	if previous != nil {
		out = *previous
		out.Extensions = copyClientExtensions(previous.Extensions)
	}
	if observed != nil {
		if observed.ClientType != 0 {
			out.ClientType = observed.ClientType
		}
		mergeClientSoftware(&out.OSName, &out.OSVersion, observed.OSName, observed.OSVersion)
		mergeClientSoftware(&out.BrowserName, &out.BrowserVersion, observed.BrowserName, observed.BrowserVersion)
		if observed.AppName != "" && observed.AppName != out.AppName {
			out.AppBuild = ""
		}
		mergeClientSoftware(&out.AppName, &out.AppVersion, observed.AppName, observed.AppVersion)
		if observed.DeviceType != 0 {
			out.DeviceType = observed.DeviceType
		}
		if observed.DeviceModel != "" {
			out.DeviceModel = observed.DeviceModel
		}
		if observed.DeviceManufacturer != "" {
			out.DeviceManufacturer = observed.DeviceManufacturer
		}
		if observed.AppBuild != "" {
			out.AppBuild = observed.AppBuild
		}
		if len(observed.Extensions) > 0 {
			if out.Extensions == nil {
				out.Extensions = make(map[string]json.RawMessage)
			}
			for k, v := range observed.Extensions {
				out.Extensions[k] = append(json.RawMessage(nil), v...)
			}
		}
	}
	if clientInfoEmpty(&out) {
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
	if observedVersion != "" {
		*version = observedVersion
	}
}

func clientInfoEmpty(v *ClientInfo) bool {
	return v.ClientType == 0 && v.OSName == "" && v.OSVersion == "" && v.BrowserName == "" && v.BrowserVersion == "" && v.AppName == "" && v.AppVersion == "" && v.AppBuild == "" && v.DeviceType == 0 && v.DeviceModel == "" && v.DeviceManufacturer == "" && len(v.Extensions) == 0
}

func copyClientExtensions(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
