// Package clientinfo 将可选的显式头、UA Client Hints 和 User-Agent 转换为
// 有界、自报的客户端观察值。解析完全本地进行，不构成凭证或设备证明。
package clientinfo

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/dunglas/httpsfv"
	"github.com/mileusna/useragent"
)

const automaticInputLimit = 2048

// Parse 校验显式描述，按显式值 > Client Hints > UA 补充缺失字段。
// 显式非法值返回 user.ErrInvalidClientInfo；自动输入失败只忽略该输入。
// 返回本次请求的完整规范化观察值，版本可单独出现；持久化时整体替换。
// 不读取任意转发头，不修改请求，不保存原始 UA，也不请求额外高熵提示。
func Parse(r *http.Request) (*user.ClientInfo, error) {
	if r == nil {
		return nil, nil
	}
	explicit, err := parseExplicit(r.Header)
	if err != nil {
		return nil, err
	}
	out := overlay(parseUA(r.Header), parseHints(r.Header))
	out = overlay(out, explicit)
	return user.NormalizeClientInfo(out)
}

func parseExplicit(h http.Header) (*user.ClientInfo, error) {
	out := &user.ClientInfo{}
	for _, field := range []struct {
		key    string
		target *string
	}{
		{"X-Client-OS-Name", &out.OSName}, {"X-Client-OS-Version", &out.OSVersion},
		{"X-Client-Browser-Name", &out.BrowserName}, {"X-Client-Browser-Version", &out.BrowserVersion},
		{"X-Client-App-Name", &out.AppName}, {"X-Client-App-Version", &out.AppVersion}, {"X-Device-Model", &out.DeviceModel},
	} {
		values := h.Values(field.key)
		if len(values) > 1 {
			return nil, user.ErrInvalidClientInfo
		}
		if len(values) == 1 {
			*field.target = values[0]
		}
	}
	for _, key := range []string{"X-Client-Type", "X-Device-Type"} {
		values := h.Values(key)
		if len(values) > 1 {
			return nil, user.ErrInvalidClientInfo
		}
		if len(values) == 0 || values[0] == "" {
			continue
		}
		var err error
		switch key {
		case "X-Client-Type":
			out.ClientType, err = enum.ParseClientType(values[0])
		case "X-Device-Type":
			out.DeviceType, err = enum.ParseDeviceType(values[0])
		}
		if err != nil {
			return nil, user.ErrInvalidClientInfo
		}
	}
	return user.NormalizeClientInfo(out)
}

// overlay 只合并同一请求的不同来源；类型不限制 app/browser 共存。
func overlay(lower, higher *user.ClientInfo) *user.ClientInfo {
	return user.MergeClientInfo(lower, higher)
}

func parseUA(h http.Header) *user.ClientInfo {
	values := h.Values("User-Agent")
	if len(values) != 1 || len(values[0]) > automaticInputLimit || !validText(values[0], automaticInputLimit) || !balancedComments(values[0]) {
		return nil
	}
	ua := useragent.Parse(values[0])
	// 上游允许把任意客户端库名称当作 Name；只输出明确识别的浏览器。
	if ua.Bot || !browserName(ua.Name) {
		return nil
	}
	out := &user.ClientInfo{ClientType: enum.ClientWeb, OSName: automaticText(ua.OS, 64), OSVersion: automaticText(ua.OSVersion, 32), BrowserName: ua.Name, BrowserVersion: automaticText(ua.Version, 32), DeviceModel: automaticText(ua.Device, 64)}
	// Safari/ 后是引擎构建版本，只有 Version/ 才是可展示浏览器版本。
	if ua.Name == "Safari" && !strings.Contains(values[0], "Version/") {
		out.BrowserVersion = ""
	}
	switch {
	case ua.Tablet:
		out.DeviceType = enum.DeviceTablet
	case ua.Mobile:
		out.DeviceType = enum.DevicePhone
	case ua.Desktop:
		out.DeviceType = enum.DeviceDesktop
	}
	return out
}

func browserName(name string) bool {
	switch name {
	case "Chrome", "Chromium", "Safari", "Firefox", "Edge", "Opera", "Opera Mini", "Opera Touch", "Internet Explorer", "Samsung Browser", "Vivaldi", "Huawei Browser":
		return true
	default:
		return false
	}
}

func balancedComments(s string) bool {
	depth := 0
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if depth > 0 && r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !escaped
}

func parseHints(h http.Header) *user.ClientInfo {
	// 包含所有 Sec-CH-UA* 输入的总量，包括未使用的高熵字段和重复值。
	total := 0
	for key, values := range h {
		if !strings.HasPrefix(strings.ToLower(key), "sec-ch-ua") {
			continue
		}
		for _, v := range values {
			total += len(v)
			if total > automaticInputLimit {
				return nil
			}
		}
	}
	out := &user.ClientInfo{}
	name, version := hintBrowser(h, "Sec-CH-UA")
	fullName, fullVersion := hintBrowser(h, "Sec-CH-UA-Full-Version-List")
	if fullName != "" && (name == "" || fullName == name) {
		name, version = fullName, fullVersion
	}
	if name != "" {
		out.ClientType = enum.ClientWeb
		out.BrowserName = name
		out.BrowserVersion = version
	}
	out.OSName = hintString(h, "Sec-CH-UA-Platform", 64)
	out.OSVersion = hintString(h, "Sec-CH-UA-Platform-Version", 32)
	out.DeviceModel = hintString(h, "Sec-CH-UA-Model", 64)
	if value, ok := hintItem(h, "Sec-CH-UA-Mobile"); ok {
		if mobile, ok := value.(bool); ok {
			if mobile {
				out.DeviceType = enum.DevicePhone
			} else {
				// ?0 也包括平板；只有明确的桌面平台才补 DESKTOP。
				switch out.OSName {
				case "Windows", "macOS", "Linux", "Chrome OS":
					out.DeviceType = enum.DeviceDesktop
				}
			}
		}
	}
	return out
}

func hintItem(h http.Header, key string) (any, bool) {
	values := h.Values(key)
	if len(values) != 1 {
		return nil, false
	}
	item, err := httpsfv.UnmarshalItem(values)
	if err != nil {
		return nil, false
	}
	return item.Value, true
}

func hintString(h http.Header, key string, limit int) string {
	value, ok := hintItem(h, key)
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return automaticText(text, limit)
}

func hintBrowser(h http.Header, key string) (string, string) {
	values := h.Values(key)
	if len(values) != 1 {
		return "", ""
	}
	list, err := httpsfv.UnmarshalList(values)
	if err != nil {
		return "", ""
	}
	type candidate struct {
		name, version string
		rank          int
	}
	best := candidate{}
	seen := map[string]bool{}
	for _, member := range list {
		item, ok := member.(httpsfv.Item)
		if !ok {
			return "", ""
		}
		brand, ok := item.Value.(string)
		if !ok {
			return "", ""
		}
		value, ok := item.Params.Get("v")
		if !ok {
			return "", ""
		}
		version, ok := value.(string)
		if !ok || !validText(version, 32) {
			return "", ""
		}
		if seen[brand] {
			return "", ""
		}
		seen[brand] = true
		current := candidate{version: strings.TrimSpace(version)}
		// 已知产品优先于其兼容品牌；未知品牌（包括 GREASE）不被选为浏览器。
		switch brand {
		case "Microsoft Edge":
			current.name, current.rank = "Edge", 4
		case "Opera":
			current.name, current.rank = "Opera", 3
		case "Google Chrome":
			current.name, current.rank = "Chrome", 2
		case "Chromium":
			current.name, current.rank = "Chromium", 1
		}
		if current.rank > best.rank {
			best = current
		}
	}
	return best.name, best.version
}

func automaticText(s string, limit int) string {
	if !validText(s, limit) {
		return ""
	}
	return strings.TrimSpace(s)
}

func validText(s string, limit int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(strings.TrimSpace(s)) > limit {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
