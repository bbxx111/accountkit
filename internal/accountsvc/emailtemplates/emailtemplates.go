// Package emailtemplates 解析 accountsvc 部署方提供的验证码邮件纯文本模板，并实现 smtp.Renderer。
// 模板在启动时编译为字面量与占位符片段，渲染只做一次拼接：不执行模板逻辑，替换值不再展开。
// 错误只含字段位置与固定分类，不回显文件内容。
package emailtemplates

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/boundedjson"
	"github.com/bbxx111/accountkit/internal/mailtext"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

const (
	// MaxFileBytes 是模板文件的字节上限。
	MaxFileBytes = 64 * 1024
	// MaxSubjectRunes、MaxTextRunes 按模板原文的 Unicode 码位计数。
	MaxSubjectRunes = 64
	MaxTextRunes    = 4096
	// MaxPlaceholders 是单个模板允许的占位符个数；转义的花括号不计入。
	MaxPlaceholders = 8
	// Version 是当前唯一支持的文件格式版本。
	Version = 1
)

type placeholder uint8

const (
	literal placeholder = iota
	code
	ttlMinutes
	brand
)

var placeholderNames = map[string]placeholder{"code": code, "ttl_minutes": ttlMinutes, "brand": brand}

type segment struct {
	kind placeholder
	text string // 仅 literal 使用
}

type template []segment

func (t template) uses(p placeholder) bool {
	for _, s := range t {
		if s.kind == p {
			return true
		}
	}
	return false
}

type purposeTemplates struct{ subject, text template }

// Renderer 只能由 Parse 构造；零值与 nil 渲染均返回 sender.ErrUnavailable。构造后只读，可并发使用。
type Renderer struct {
	brand     string
	templates map[enum.CodePurpose]purposeTemplates
}

var purposes = []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete}

func fail(location, category string) error { return errors.New(location + ": " + category) }

// Parse 严格解析模板文件内容。brand 为 {brand} 的取值，须满足 smtp.ValidateBrand；
// 为空时任何模板引用 {brand} 都视为错误。
func Parse(raw []byte, brandValue string) (*Renderer, error) {
	if smtp.ValidateBrand(brandValue) != nil {
		return nil, fail("(root)", "invalid brand")
	}
	if len(raw) == 0 {
		return nil, fail("(root)", "empty file")
	}
	// boundedjson 拒绝重复键、尾随内容、非法 UTF-8 与孤立代理项，并限制大小、深度与节点数；
	// 深度 4 对应 根对象 → templates → 用途对象 → 字符串。
	value, err := boundedjson.Decode(raw, boundedjson.Limits{MaxBytes: MaxFileBytes, MaxDepth: 4, MaxNodes: 64})
	if err != nil {
		return nil, fail("(root)", "invalid JSON")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fail("(root)", "not an object")
	}
	if !onlyKeys(root, "version", "templates") {
		return nil, fail("(root)", "unknown field")
	}
	if version, ok := root["version"].(json.Number); !ok || version.String() != strconv.Itoa(Version) {
		return nil, fail("version", "must be 1")
	}
	entries, ok := root["templates"].(map[string]any)
	if !ok {
		return nil, fail("templates", "must be an object")
	}
	names := make([]string, len(purposes))
	for i, p := range purposes {
		names[i] = p.String()
	}
	if !onlyKeys(entries, names...) {
		return nil, fail("templates", "unknown purpose")
	}
	r := &Renderer{brand: brandValue, templates: make(map[enum.CodePurpose]purposeTemplates, len(purposes))}
	for _, p := range purposes {
		location := "templates." + p.String()
		entry, ok := entries[p.String()].(map[string]any)
		if !ok {
			return nil, fail(location, "missing or not an object")
		}
		if !onlyKeys(entry, "subject", "text") {
			return nil, fail(location, "unknown field")
		}
		subject, err := compileField(entry, "subject", location+".subject", brandValue)
		if err != nil {
			return nil, err
		}
		text, err := compileField(entry, "text", location+".text", brandValue)
		if err != nil {
			return nil, err
		}
		r.templates[p] = purposeTemplates{subject: subject, text: text}
	}
	return r, nil
}

func onlyKeys(m map[string]any, allowed ...string) bool {
	for key := range m {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func compileField(entry map[string]any, field, location, brandValue string) (template, error) {
	raw, ok := entry[field].(string)
	if !ok {
		return nil, fail(location, "missing or not a string")
	}
	isSubject := field == "subject"
	limit, valid := MaxTextRunes, mailtext.BodyText(raw)
	if isSubject {
		limit, valid = MaxSubjectRunes, mailtext.HeaderText(raw)
	}
	if !valid {
		return nil, fail(location, "empty or forbidden characters")
	}
	if utf8.RuneCountInString(raw) > limit {
		return nil, fail(location, "too long")
	}
	t, err := compile(raw)
	if err != nil {
		return nil, fail(location, err.Error())
	}
	switch {
	case isSubject && t.uses(code):
		return nil, fail(location, "{code} not allowed in subject")
	case !isSubject && !t.uses(code):
		return nil, fail(location, "missing {code}")
	case t.uses(brand) && brandValue == "":
		return nil, fail(location, "{brand} used but brand is empty")
	}
	return t, nil
}

// compile 返回的错误只含固定分类。
func compile(raw string) (template, error) {
	var t template
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t = append(t, segment{kind: literal, text: lit.String()})
			lit.Reset()
		}
	}
	placeholders := 0
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '{':
			if strings.HasPrefix(raw[i:], "{{") {
				lit.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(raw[i+1:], '}')
			if end < 0 {
				return nil, errors.New("unclosed placeholder")
			}
			name := raw[i+1 : i+1+end]
			if name == "" {
				return nil, errors.New("empty placeholder")
			}
			kind, ok := placeholderNames[name]
			if !ok {
				return nil, errors.New("unknown placeholder")
			}
			if placeholders++; placeholders > MaxPlaceholders {
				return nil, errors.New("too many placeholders")
			}
			flush()
			t = append(t, segment{kind: kind})
			i += end + 2
		case '}':
			if !strings.HasPrefix(raw[i:], "}}") {
				return nil, errors.New("unmatched }")
			}
			lit.WriteByte('}')
			i += 2
		default:
			lit.WriteByte(raw[i])
			i++
		}
	}
	flush()
	return t, nil
}

func (t template) render(values *[brand + 1]string) string {
	var b strings.Builder
	for _, s := range t {
		if s.kind == literal {
			b.WriteString(s.text)
		} else {
			b.WriteString(values[s.kind])
		}
	}
	return b.String()
}

// Render 实现 smtp.Renderer。失败一律返回 sender.ErrUnavailable，不记录日志。
func (r *Renderer) Render(ctx context.Context, m sender.CodeMessage) (smtp.EmailContent, error) {
	if r == nil || ctx.Err() != nil || !validMessage(m) {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	t, ok := r.templates[m.Purpose]
	if !ok {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	values := [brand + 1]string{
		code:       m.Code,
		ttlMinutes: strconv.FormatFloat(math.Ceil(m.TTL.Minutes()), 'f', 0, 64),
		brand:      r.brand,
	}
	return smtp.EmailContent{Subject: t.subject.render(&values), Text: t.text.render(&values)}, nil
}

func validMessage(m sender.CodeMessage) bool {
	if !m.Purpose.Valid() || m.TTL <= 0 || m.Code == "" {
		return false
	}
	for _, c := range m.Code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
