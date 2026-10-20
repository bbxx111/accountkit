package user

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
)

type clientSoftwareJSON struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}
type clientAppJSON struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
}
type clientDeviceJSON struct {
	Type         string `json:"type,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
}
type clientInfoJSON struct {
	Type       string                     `json:"type,omitempty"`
	OS         *clientSoftwareJSON        `json:"os,omitempty"`
	Device     *clientDeviceJSON          `json:"device,omitempty"`
	App        *clientAppJSON             `json:"app,omitempty"`
	Browser    *clientSoftwareJSON        `json:"browser,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

func clientInfoJSONFrom(v *ClientInfo) clientInfoJSON {
	out := clientInfoJSON{Extensions: v.Extensions}
	if v.ClientType.Valid() {
		out.Type = v.ClientType.String()
	}
	if v.OSName != "" || v.OSVersion != "" {
		out.OS = &clientSoftwareJSON{v.OSName, v.OSVersion}
	}
	if v.DeviceType != 0 || v.DeviceManufacturer != "" || v.DeviceModel != "" {
		out.Device = &clientDeviceJSON{Manufacturer: v.DeviceManufacturer, Model: v.DeviceModel}
		if v.DeviceType.Valid() {
			out.Device.Type = v.DeviceType.String()
		}
	}
	if v.AppName != "" || v.AppVersion != "" || v.AppBuild != "" {
		out.App = &clientAppJSON{v.AppName, v.AppVersion, v.AppBuild}
	}
	if v.BrowserName != "" || v.BrowserVersion != "" {
		out.Browser = &clientSoftwareJSON{v.BrowserName, v.BrowserVersion}
	}
	return out
}

// MarshalClientInfo 校验、规范化并编码完整的有界嵌套 JSON；空描述编码为 null。
func MarshalClientInfo(input *ClientInfo) ([]byte, error) {
	v, err := NormalizeClientInfo(input)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(clientInfoJSONFrom(v))
}

// UnmarshalClientInfo 严格读取嵌套 JSON，拒绝未知字段、重复键和损坏的描述。
// 空对象表示无观察值；JSON null 不是可存储的描述对象。
func UnmarshalClientInfo(input []byte) (*ClientInfo, error) {
	v, err := decodeClientJSON(input)
	if err != nil {
		return nil, err
	}
	root, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalidClientInfo
	}
	out := &ClientInfo{}
	for key, value := range root {
		switch key {
		case "type":
			s, ok := value.(string)
			if !ok {
				return nil, ErrInvalidClientInfo
			}
			out.ClientType, err = enum.ParseClientType(s)
			if err != nil {
				return nil, ErrInvalidClientInfo
			}
		case "os", "browser", "app", "device":
			obj, ok := value.(map[string]any)
			if !ok {
				return nil, ErrInvalidClientInfo
			}
			for field, value := range obj {
				s, ok := value.(string)
				if !ok {
					return nil, ErrInvalidClientInfo
				}
				switch key + "." + field {
				case "os.name":
					out.OSName = s
				case "os.version":
					out.OSVersion = s
				case "browser.name":
					out.BrowserName = s
				case "browser.version":
					out.BrowserVersion = s
				case "app.name":
					out.AppName = s
				case "app.version":
					out.AppVersion = s
				case "app.build":
					out.AppBuild = s
				case "device.manufacturer":
					out.DeviceManufacturer = s
				case "device.model":
					out.DeviceModel = s
				case "device.type":
					out.DeviceType, err = enum.ParseDeviceType(s)
					if err != nil {
						return nil, ErrInvalidClientInfo
					}
				default:
					return nil, ErrInvalidClientInfo
				}
			}
		case "extensions":
			obj, ok := value.(map[string]any)
			if !ok {
				return nil, ErrInvalidClientInfo
			}
			out.Extensions = make(map[string]json.RawMessage, len(obj))
			for k, v := range obj {
				out.Extensions[k], err = json.Marshal(v)
				if err != nil {
					return nil, ErrInvalidClientInfo
				}
			}
		default:
			return nil, ErrInvalidClientInfo
		}
	}
	return NormalizeClientInfo(out)
}

// 在构造 map 前检查重复键，数值保留 JSON 字面量精度。
func decodeClientJSON(input []byte) (any, error) {
	if len(input) > 16*1024 || !utf8.Valid(input) {
		return nil, ErrInvalidClientInfo
	}
	d := json.NewDecoder(bytes.NewReader(input))
	d.UseNumber()
	v, err := readClientJSONValue(d, 0)
	if err != nil {
		return nil, ErrInvalidClientInfo
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalidClientInfo
	}
	return v, nil
}

func readClientJSONValue(d *json.Decoder, depth int) (any, error) {
	// 核心对象和 extensions 根对象之外，只允许四层扩展值。
	if depth > 6 {
		return nil, ErrInvalidClientInfo
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case string:
		if !validClientJSONString(v) {
			return nil, ErrInvalidClientInfo
		}
		return v, nil
	case json.Delim:
		switch v {
		case '{':
			out := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok || !validClientJSONString(k) {
					return nil, ErrInvalidClientInfo
				}
				if _, exists := out[k]; exists {
					return nil, ErrInvalidClientInfo
				}
				out[k], err = readClientJSONValue(d, depth+1)
				if err != nil {
					return nil, err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidClientInfo
			}
			return out, nil
		case '[':
			out := []any{}
			for d.More() {
				value, err := readClientJSONValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidClientInfo
			}
			return out, nil
		default:
			return nil, ErrInvalidClientInfo
		}
	default:
		return t, nil
	}
}

func validClientJSONString(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func normalizeClientExtensions(in map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > 64 {
		return nil, ErrInvalidClientInfo
	}
	out := make(map[string]json.RawMessage, len(in))
	nodes := 0
	for key, raw := range in {
		if !validClientExtensionKey(key) {
			return nil, ErrInvalidClientInfo
		}
		value, err := decodeClientJSON(raw)
		if err != nil {
			return nil, ErrInvalidClientInfo
		}
		value, err = normalizeClientExtensionValue(value, 1, &nodes)
		if err != nil {
			return nil, err
		}
		out[key], err = json.Marshal(value)
		if err != nil {
			return nil, ErrInvalidClientInfo
		}
	}
	return out, nil
}

func normalizeClientExtensionValue(v any, depth int, nodes *int) (any, error) {
	(*nodes)++
	if *nodes > 64 || depth > 4 {
		return nil, ErrInvalidClientInfo
	}
	switch value := v.(type) {
	case string:
		if utf8.RuneCountInString(value) > 256 {
			return nil, ErrInvalidClientInfo
		}
	case json.Number:
		return normalizeClientNumber(value)
	case map[string]any:
		for k, child := range value {
			if !validClientExtensionKey(k) {
				return nil, ErrInvalidClientInfo
			}
			var err error
			value[k], err = normalizeClientExtensionValue(child, depth+1, nodes)
			if err != nil {
				return nil, err
			}
		}
	case []any:
		if len(value) > 16 {
			return nil, ErrInvalidClientInfo
		}
		for i, child := range value {
			var err error
			value[i], err = normalizeClientExtensionValue(child, depth+1, nodes)
			if err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

// JSONB会将指数展开为十进制。先计算展开长度，再分配最多2048字节，
// 避免短指数输入绕过完整JSON上限或触发无界内存分配。全程不转浮点。
func normalizeClientNumber(number json.Number) (json.Number, error) {
	s := number.String()
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	mantissa, exponent := s, ""
	if index := strings.IndexAny(s, "eE"); index >= 0 {
		mantissa, exponent = s[:index], s[index+1:]
	}
	point := strings.IndexByte(mantissa, '.')
	if point < 0 {
		point = len(mantissa)
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	significant := strings.TrimLeft(digits, "0")
	if significant == "" {
		return json.Number("0"), nil
	}
	leading := len(digits) - len(significant)
	significant = strings.TrimRight(significant, "0")
	power := int64(0)
	if exponent != "" {
		var err error
		power, err = strconv.ParseInt(exponent, 10, 32)
		if err != nil {
			return "", ErrInvalidClientInfo
		}
	}
	decimalPoint := int64(point-leading) + power
	length := int64(len(significant))
	switch {
	case decimalPoint <= 0:
		length += 2 - decimalPoint
	case decimalPoint >= length:
		length = decimalPoint
	default:
		length++
	}
	if negative {
		length++
	}
	if length > 2048 {
		return "", ErrInvalidClientInfo
	}
	var out string
	switch {
	case decimalPoint <= 0:
		out = "0." + strings.Repeat("0", int(-decimalPoint)) + significant
	case decimalPoint >= int64(len(significant)):
		out = significant + strings.Repeat("0", int(decimalPoint)-len(significant))
	default:
		out = significant[:int(decimalPoint)] + "." + significant[int(decimalPoint):]
	}
	if negative {
		out = "-" + out
	}
	return json.Number(out), nil
}

func validClientExtensionKey(key string) bool {
	if len(key) < 1 || len(key) > 64 || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	// 固定敏感名称和下划线分隔词段，可独立审查；不检查任意值中的隐私，
	// 也不替代宿主的扩展语义白名单。
	for _, part := range strings.Split(key, "_") {
		switch part {
		case "password", "passwd", "secret", "token", "credential", "credentials", "authorization", "cookie", "cookies", "captcha", "otp", "ip", "address", "location", "latitude", "longitude", "imei", "mac", "ua":
			return false
		}
	}
	for _, name := range []string{"user_agent", "device_id", "hardware_id", "verification_code", "sms_code", "email_code", "one_time_code", "security_code"} {
		if key == name || strings.HasPrefix(key, name+"_") || strings.HasSuffix(key, "_"+name) || strings.Contains(key, "_"+name+"_") {
			return false
		}
	}
	return true
}
