package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const MaxEventBytes = 64 * 1024
const MaxResources = 32

var sourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
var actionPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var hexPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// NormalizedActions returns an independent sorted, unique action set. Empty
// means no action filter. Invalid identifiers are rejected, never rewritten.
func (q Query) NormalizedActions() ([]string, error) {
	actions := slices.Clone(q.Actions)
	for _, action := range actions {
		if len(action) > 128 || !actionPattern.MatchString(action) {
			return nil, invalid("actions")
		}
	}
	slices.Sort(actions)
	return slices.Compact(actions), nil
}

// Validate 拒绝非法或超限内容；错误仅包含字段位置，不包含输入值。
func Validate(e Event) error { _, err := Freeze(e); return err }

// Freeze 校验、深拷贝和规范化事件，不生成或替换事件身份。
// 时间统一为 UTC 微秒，资源按完整引用排序去重；空 data/targets 规范为 {}/[]。
func Freeze(e Event) (Event, error) {
	if e.ID == uuid.Nil || e.ID.Version() != 7 || e.ID.Variant() != uuid.RFC4122 {
		return Event{}, invalid("id")
	}
	if len(e.Source) > 128 || !sourcePattern.MatchString(e.Source) {
		return Event{}, invalid("source")
	}
	if len(e.Action) > 128 || !actionPattern.MatchString(e.Action) {
		return Event{}, invalid("action")
	}
	if e.Result != nil {
		if !e.Result.Valid() {
			return Event{}, invalid("result")
		}
		r := *e.Result
		e.Result = &r
	}
	if err := validateActor(e.Actor); err != nil {
		return Event{}, err
	}
	e.OccurTime = e.OccurTime.UTC().Truncate(time.Microsecond)
	if e.OccurTime.IsZero() || e.OccurTime.Year() < 1 || e.OccurTime.Year() > 9999 {
		return Event{}, invalid("occur_time")
	}
	var err error
	e.Targets, err = freezeResources(e.Targets)
	if err != nil {
		return Event{}, err
	}
	e.Context.RelatedResources, err = freezeResources(e.Context.RelatedResources)
	if err != nil {
		return Event{}, err
	}
	if err := validateContext(e.Context); err != nil {
		return Event{}, err
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage(`{}`)
	}
	data, err := parseJSON(e.Data)
	if err != nil {
		return Event{}, invalid("data")
	}
	if _, ok := data.(map[string]any); !ok {
		return Event{}, invalid("data")
	}
	e.Data, err = json.Marshal(data)
	if err != nil {
		return Event{}, invalid("data")
	}
	wire, err := json.Marshal(e)
	if err != nil || len(wire) > MaxEventBytes {
		return Event{}, invalid("event_size")
	}
	return e, nil
}

// DecodeEvent 对所有嵌套对象拒绝重复键，再按通用事件契约校验。
// 未知可选字段可以忽略；生产事件不能提供存储生成的 create_time。
func DecodeEvent(wire []byte) (Event, error) {
	v, err := parseJSON(wire)
	if err != nil {
		return Event{}, invalid("json")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return Event{}, invalid("event")
	}
	if _, ok := obj["create_time"]; ok {
		return Event{}, invalid("create_time")
	}
	for _, key := range []string{"actor", "context", "data"} {
		if value, exists := obj[key]; exists {
			if _, ok := value.(map[string]any); !ok {
				return Event{}, invalid(key)
			}
		}
	}
	if value, exists := obj["targets"]; exists {
		if _, ok := value.([]any); !ok {
			return Event{}, invalid("targets")
		}
	}
	var e Event
	if err := unmarshalEvent(obj, &e); err != nil {
		return Event{}, invalid("event")
	}
	return Freeze(e)
}

// Scrub 返回独立副本，清理名称和客户端隐私信息，保留稳定身份和追踪关联。
// 业务 data 的清理由对应领域定义，本包不识别认证或其他业务命名空间。
func Scrub(e Event) Event {
	e.Actor.Name = ""
	e.Targets = slices.Clone(e.Targets)
	e.Context.RelatedResources = slices.Clone(e.Context.RelatedResources)
	for i := range e.Targets {
		e.Targets[i].Name = ""
	}
	for i := range e.Context.RelatedResources {
		e.Context.RelatedResources[i].Name = ""
	}
	e.Context.ClientIP, e.Context.DeviceID, e.Context.UserAgent = "", "", ""
	e.Data = slices.Clone(e.Data)
	if e.Result != nil {
		r := *e.Result
		e.Result = &r
	}
	return e
}

// Equivalent 严格比较规范化后的逻辑内容，包括隐私字段。
// 隐私清理后的重放须由领域显式对双方应用同一清理投影再比较。
func Equivalent(a, b Event) bool {
	a, err := Freeze(a)
	if err != nil {
		return false
	}
	b, err = Freeze(b)
	if err != nil {
		return false
	}
	av, err := parseJSON(a.Data)
	if err != nil {
		return false
	}
	bv, err := parseJSON(b.Data)
	if err != nil {
		return false
	}
	a.Data, b.Data = nil, nil
	return reflect.DeepEqual(a, b) && jsonEqual(av, bv)
}

func invalid(field string) error { return fmt.Errorf("audit/event: invalid %s", field) }

func validText(s string, maxBytes int) bool {
	return len(s) <= maxBytes && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func validName(s string) bool {
	return validText(s, 256*utf8.UTFMax) && utf8.RuneCountInString(s) <= 256
}

func validateActor(a Actor) error {
	switch a.Type {
	case ActorUser, ActorService, ActorSystem:
		if a.Namespace == "" || a.ID == "" {
			return invalid("actor.identity")
		}
	case ActorAnonymous, ActorUnknown:
		if a.Namespace != "" || a.ID != "" {
			return invalid("actor.identity")
		}
	default:
		return invalid("actor.type")
	}
	if !validText(a.Namespace, 512) || !validText(a.ID, 512) || !validName(a.Name) {
		return invalid("actor")
	}
	return nil
}

func freezeResources(in []Resource) ([]Resource, error) {
	if len(in) > MaxResources {
		return nil, invalid("resources")
	}
	out := make([]Resource, len(in))
	copy(out, in)
	for _, r := range out {
		if len(r.Type) > 128 || !actionPattern.MatchString(r.Type) || r.Namespace == "" || r.ID == "" || !validText(r.Namespace, 512) || !validText(r.ID, 512) || !validName(r.Name) {
			return nil, invalid("resource")
		}
	}
	slices.SortFunc(out, func(a, b Resource) int {
		for _, pair := range [][2]string{{a.Namespace, b.Namespace}, {a.Type, b.Type}, {a.ID, b.ID}, {a.Name, b.Name}} {
			if c := strings.Compare(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		return 0
	})
	return slices.Compact(out), nil
}

func validateContext(c EventContext) error {
	if !validText(c.RequestID, 128) || !validText(c.UserAgent, 2048) || !validText(c.DeviceID, 256) {
		return invalid("context")
	}
	if c.TraceID != "" && !validHex(c.TraceID, 32) {
		return invalid("context.trace_id")
	}
	if c.SpanID != "" && (c.TraceID == "" || !validHex(c.SpanID, 16)) {
		return invalid("context.span_id")
	}
	if c.ClientIP != "" {
		ip, err := netip.ParseAddr(c.ClientIP)
		if err != nil || ip.Zone() != "" {
			return invalid("context.client_ip")
		}
	}
	return nil
}

func validHex(s string, length int) bool {
	return len(s) == length && hexPattern.MatchString(s) && s != strings.Repeat("0", length)
}

// parseJSON 使用 Token 而非 map 解码，以免重复键被覆盖；Number 保留整数精度。
func parseJSON(wire []byte) (any, error) {
	return parseJSONLimit(wire, MaxEventBytes)
}

func parseJSONLimit(wire []byte, limit int) (any, error) {
	if len(wire) > limit || !utf8.Valid(wire) {
		return nil, errors.New("invalid JSON size or encoding")
	}
	if err := validateSurrogates(wire); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(wire))
	d.UseNumber()
	budget := jsonBudget{remaining: int64(limit)}
	v, err := jsonValue(d, &budget)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}

// encoding/json 会把未配对的 UTF-16 surrogate 替换成 U+FFFD，JSONB 则拒绝它们。
// 在解码前核验 escape，防止非法输入被不可逆地改写为另一个事件。
func validateSurrogates(wire []byte) error {
	quoted := false
	for i := 0; i < len(wire); i++ {
		if wire[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || wire[i] != '\\' {
			continue
		}
		if i+1 >= len(wire) {
			return errors.New("invalid escape")
		}
		if wire[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(wire) {
			return errors.New("invalid unicode escape")
		}
		code, err := strconv.ParseUint(string(wire[i+2:i+6]), 16, 16)
		if err != nil {
			return errors.New("invalid unicode escape")
		}
		if code >= 0xdc00 && code <= 0xdfff {
			return errors.New("unpaired surrogate")
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+12 > len(wire) || wire[i+6] != '\\' || wire[i+7] != 'u' {
				return errors.New("unpaired surrogate")
			}
			low, err := strconv.ParseUint(string(wire[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("unpaired surrogate")
			}
			i += 11
		} else {
			i += 5
		}
	}
	return nil
}

// jsonBudget 是整棵规范化 JSON 树共享的序列化字节预算。
// 容器、键、字符串和数值均先扣减，再保存或展开；最终事件仍另做完整检查。
type jsonBudget struct{ remaining int64 }

func (b *jsonBudget) consume(n int64) error {
	if n > b.remaining {
		return errors.New("normalized JSON exceeds size budget")
	}
	b.remaining -= n
	return nil
}

// jsonStringBytes 与 encoding/json 的默认 HTML escaping 和控制字符编码一致，
// 计算长度而不为长度检查分配另一份已转义字符串。
func jsonStringBytes(s string) int64 {
	n := int64(2)
	for _, r := range s {
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			n += 2
		case '<', '>', '&', '\u2028', '\u2029':
			n += 6
		default:
			if r < 0x20 {
				n += 6
			} else {
				n += int64(utf8.RuneLen(r))
			}
		}
	}
	return n
}

func jsonValue(d *json.Decoder, budget *jsonBudget) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case json.Delim:
		if err := budget.consume(2); err != nil {
			return nil, err
		}
		switch v {
		case '{':
			obj := make(map[string]any)
			for d.More() {
				if len(obj) != 0 {
					if err := budget.consume(1); err != nil {
						return nil, err
					}
				}
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				s, ok := key.(string)
				if !ok || !validText(s, MaxEventBytes) {
					return nil, errors.New("invalid key")
				}
				if _, exists := obj[s]; exists {
					return nil, errors.New("duplicate key")
				}
				if err := budget.consume(jsonStringBytes(s) + 1); err != nil {
					return nil, err
				}
				value, err := jsonValue(d, budget)
				if err != nil {
					return nil, err
				}
				obj[s] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errors.New("invalid object")
			}
			return obj, nil
		case '[':
			array := make([]any, 0)
			for d.More() {
				if len(array) != 0 {
					if err := budget.consume(1); err != nil {
						return nil, err
					}
				}
				value, err := jsonValue(d, budget)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errors.New("invalid array")
			}
			return array, nil
		default:
			return nil, errors.New("invalid delimiter")
		}
	case string:
		if !validText(v, MaxEventBytes) {
			return nil, errors.New("invalid string")
		}
		if err := budget.consume(jsonStringBytes(v)); err != nil {
			return nil, err
		}
	case json.Number:
		return decimalNumber(v, budget)
	case bool:
		n := int64(5)
		if v {
			n = 4
		}
		if err := budget.consume(n); err != nil {
			return nil, err
		}
	case nil:
		if err := budget.consume(4); err != nil {
			return nil, err
		}
	}
	return token, nil
}

// decimalNumber 与 JSONB 的普通十进制表示对齐；先计算长度再展开指数，
// 防止很短的指数输入在存储或回读时扩大到超过完整事件限额。
func decimalNumber(n json.Number, budget *jsonBudget) (json.Number, error) {
	normal, err := normalizedNumber(n)
	if err != nil {
		return "", err
	}
	if normal == "0" {
		if err := budget.consume(1); err != nil {
			return "", err
		}
		return json.Number("0"), nil
	}
	i := strings.LastIndexByte(normal, 'e')
	digits := normal[:i]
	exponent, err := strconv.ParseInt(normal[i+1:], 10, 64)
	if err != nil {
		return "", err
	}
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	point := int64(len(digits)) + exponent
	length := int64(len(digits) + len(sign))
	if exponent >= 0 {
		length += exponent
	} else if point > 0 {
		length++
	} else {
		length += 2 - point
	}
	if err := budget.consume(length); err != nil {
		return "", err
	}
	if exponent >= 0 {
		return json.Number(sign + digits + strings.Repeat("0", int(exponent))), nil
	}
	if point > 0 {
		return json.Number(sign + digits[:int(point)] + "." + digits[int(point):]), nil
	}
	return json.Number(sign + "0." + strings.Repeat("0", int(-point)) + digits), nil
}

// normalizedNumber 比较十进制值而不经过 float64；同时拒绝 PostgreSQL numeric 无法存储的指数。
func normalizedNumber(n json.Number) (string, error) {
	s := n.String()
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	exponent := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exponent, err = strconv.ParseInt(s[i+1:], 10, 32)
		if err != nil {
			return "", errors.New("invalid numeric exponent")
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exponent -= int64(len(s) - i - 1)
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", nil
	}
	for strings.HasSuffix(s, "0") {
		s = s[:len(s)-1]
		exponent++
	}
	if int64(len(s))+exponent > 131072 || exponent < -16383 {
		return "", errors.New("numeric exceeds PostgreSQL bounds")
	}
	if negative {
		s = "-" + s
	}
	return s + "e" + strconv.FormatInt(exponent, 10), nil
}

func jsonEqual(a, b any) bool {
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		if !ok {
			return false
		}
		an, _ := normalizedNumber(av)
		bn, _ := normalizedNumber(bv)
		return an == bn
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			w, exists := bv[k]
			if !exists || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
