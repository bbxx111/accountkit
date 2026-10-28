// Package emailtemplates 解析 accountsvc 部署方提供的验证码邮件清单，并用内置布局实现 smtp.Renderer。
// 清单只配置文字、品牌和 Logo 地址；版式由嵌入的 HTML 与纯文本布局提供。文字在启动时编译为
// 字面量与占位符片段，渲染只做一次拼接：不执行模板逻辑，替换值不再展开。错误只含字段位置与
// 固定分类，不回显文件内容。
package emailtemplates

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/boundedjson"
	"github.com/bbxx111/accountkit/internal/mailtext"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

const (
	// Version 是当前唯一支持的清单格式版本。
	Version = 1
	// MaxFileBytes 是清单的字节上限。
	MaxFileBytes = 64 * 1024
	// MaxLanguages 是清单允许的语言数。
	MaxLanguages = 8
	// MaxPlaceholders 是每段文字允许的占位符个数。
	MaxPlaceholders = 8
	// MaxSubjectRunes、MaxShortRunes、MaxLongRunes 按 Unicode 码位计数。
	MaxSubjectRunes = 64
	MaxShortRunes   = 64
	MaxLongRunes    = 1024
	// MaxLogoURLBytes 是 logo_url 的字节上限。
	MaxLogoURLBytes = 2048
	// MaxAcceptLanguageBytes 之上的 Accept-Language 被忽略；最多采用前 MaxAcceptLanguageTags 项。
	MaxAcceptLanguageBytes = 512
	MaxAcceptLanguageTags  = 8
)

//go:embed layout.html
var layoutHTML string

//go:embed layout.txt
var layoutText string

// 占位符种类。literal 之外的值同时是渲染值数组的下标。
type placeholder uint8

const (
	literal placeholder = iota
	brand
	ttlMinutes
)

var placeholderNames = map[string]placeholder{"brand": brand, "ttl_minutes": ttlMinutes}

type segment struct {
	kind placeholder
	text string // 仅 literal 使用
}

type template []segment

type compiledPurpose struct{ subject, heading, intro template }

type compiledLanguage struct {
	tag       string
	brand     string
	logoURL   string // 空表示不显示 Logo
	codeLabel template
	expiry    template
	safety    template
	footer    template // nil 表示省略页脚段
	purposes  map[enum.CodePurpose]compiledPurpose
}

// Renderer 只能由 Parse 构造；零值与 nil 渲染均返回 sender.ErrUnavailable。构造后只读，可并发使用。
type Renderer struct {
	languages []compiledLanguage // 清单顺序，默认语言在前
	tags      []language.Tag
	matcher   language.Matcher // 单语言时为 nil
}

var purposes = []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete}

func fail(location, category string) error { return errors.New(location + ": " + category) }

// Parse 严格解析清单内容。错误只含字段位置与固定分类。
func Parse(raw []byte) (*Renderer, error) {
	if len(raw) == 0 {
		return nil, fail("(root)", "empty file")
	}
	// boundedjson 拒绝重复键、尾随内容、非法 UTF-8 与孤立代理项，并限制大小、深度与节点数。
	// 深度 6：根 → languages → 语言 → templates → 用途 → 字符串。
	value, err := boundedjson.Decode(raw, boundedjson.Limits{MaxBytes: MaxFileBytes, MaxDepth: 6, MaxNodes: 512})
	if err != nil {
		return nil, fail("(root)", "invalid JSON")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fail("(root)", "not an object")
	}
	if !onlyKeys(root, "version", "default_language", "languages") {
		return nil, fail("(root)", "unknown field")
	}
	if version, ok := root["version"].(json.Number); !ok || version.String() != strconv.Itoa(Version) {
		return nil, fail("version", "must be 1")
	}
	entries, ok := root["languages"].(map[string]any)
	if !ok || len(entries) == 0 || len(entries) > MaxLanguages {
		return nil, fail("languages", "must be an object with 1 to 8 languages")
	}
	defaultTag, ok := root["default_language"].(string)
	if !ok || entries[defaultTag] == nil {
		return nil, fail("default_language", "must name one of languages")
	}
	// 先校验全部标签：不规范的标签不能出现在错误位置里。
	ordered := make([]string, 0, len(entries))
	for key := range entries {
		if !canonicalTag(key) {
			return nil, fail("languages", "invalid language tag")
		}
		if key != defaultTag {
			ordered = append(ordered, key)
		}
	}
	sortStrings(ordered)
	ordered = append([]string{defaultTag}, ordered...)
	r := &Renderer{}
	for _, tag := range ordered {
		lang, err := compileLanguage(tag, entries[tag])
		if err != nil {
			return nil, err
		}
		r.languages = append(r.languages, lang)
		r.tags = append(r.tags, language.MustParse(tag))
	}
	if len(r.tags) > 1 {
		r.matcher = language.NewMatcher(r.tags)
	}
	return r, nil
}

// canonicalTag 要求标签能被解析、不是 und 或私有用途标签，且等于其规范形式，例如 zh-Hant 而不是 zh-hant。
func canonicalTag(tag string) bool {
	if strings.HasPrefix(tag, "x-") || strings.Contains(tag, "-x-") {
		return false
	}
	parsed, err := language.Parse(tag)
	return err == nil && parsed != language.Und && parsed.String() == tag
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func compileLanguage(tag string, value any) (compiledLanguage, error) {
	location := "languages." + tag
	entry, ok := value.(map[string]any)
	if !ok {
		return compiledLanguage{}, fail(location, "not an object")
	}
	if !onlyKeys(entry, "brand", "logo_url", "common", "templates") {
		return compiledLanguage{}, fail(location, "unknown field")
	}
	lang := compiledLanguage{tag: tag, purposes: make(map[enum.CodePurpose]compiledPurpose, len(purposes))}
	if lang.brand, ok = entry["brand"].(string); !ok || smtp.ValidateBrand(lang.brand) != nil || lang.brand == "" {
		return compiledLanguage{}, fail(location+".brand", "invalid brand")
	}
	if raw, present := entry["logo_url"]; present {
		if lang.logoURL, ok = raw.(string); !ok || !validLogoURL(lang.logoURL) {
			return compiledLanguage{}, fail(location+".logo_url", "must be an https URL")
		}
	}
	common, ok := entry["common"].(map[string]any)
	if !ok {
		return compiledLanguage{}, fail(location+".common", "not an object")
	}
	if !onlyKeys(common, "code_label", "expiry", "safety", "footer") {
		return compiledLanguage{}, fail(location+".common", "unknown field")
	}
	var err error
	if lang.codeLabel, err = compileBody(common, "code_label", location+".common.code_label", MaxShortRunes); err != nil {
		return compiledLanguage{}, err
	}
	if lang.expiry, err = compileBody(common, "expiry", location+".common.expiry", MaxLongRunes); err != nil {
		return compiledLanguage{}, err
	}
	if lang.safety, err = compileBody(common, "safety", location+".common.safety", MaxLongRunes); err != nil {
		return compiledLanguage{}, err
	}
	if _, present := common["footer"]; present {
		if lang.footer, err = compileBody(common, "footer", location+".common.footer", MaxLongRunes); err != nil {
			return compiledLanguage{}, err
		}
	}
	templates, ok := entry["templates"].(map[string]any)
	if !ok {
		return compiledLanguage{}, fail(location+".templates", "not an object")
	}
	names := make([]string, len(purposes))
	for i, p := range purposes {
		names[i] = p.String()
	}
	if !onlyKeys(templates, names...) {
		return compiledLanguage{}, fail(location+".templates", "unknown purpose")
	}
	for _, p := range purposes {
		purposeLocation := location + ".templates." + p.String()
		fields, ok := templates[p.String()].(map[string]any)
		if !ok {
			return compiledLanguage{}, fail(purposeLocation, "missing or not an object")
		}
		if !onlyKeys(fields, "subject", "heading", "intro") {
			return compiledLanguage{}, fail(purposeLocation, "unknown field")
		}
		var cp compiledPurpose
		if cp.subject, err = compileSubject(fields, purposeLocation+".subject"); err != nil {
			return compiledLanguage{}, err
		}
		if cp.heading, err = compileBody(fields, "heading", purposeLocation+".heading", MaxShortRunes); err != nil {
			return compiledLanguage{}, err
		}
		if cp.intro, err = compileBody(fields, "intro", purposeLocation+".intro", MaxLongRunes); err != nil {
			return compiledLanguage{}, err
		}
		lang.purposes[p] = cp
	}
	return lang, nil
}

func validLogoURL(raw string) bool {
	if raw == "" || len(raw) > MaxLogoURLBytes || !utf8.ValidString(raw) || strings.TrimSpace(raw) != raw {
		return false
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
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

func compileSubject(fields map[string]any, location string) (template, error) {
	raw, ok := fields["subject"].(string)
	if !ok {
		return nil, fail(location, "missing or not a string")
	}
	if !mailtext.HeaderText(raw) {
		return nil, fail(location, "empty or forbidden characters")
	}
	if utf8.RuneCountInString(raw) > MaxSubjectRunes {
		return nil, fail(location, "too long")
	}
	return compile(raw, location)
}

func compileBody(fields map[string]any, field, location string, limit int) (template, error) {
	raw, ok := fields[field].(string)
	if !ok {
		return nil, fail(location, "missing or not a string")
	}
	if !mailtext.BodyText(raw) {
		return nil, fail(location, "empty or forbidden characters")
	}
	if utf8.RuneCountInString(raw) > limit {
		return nil, fail(location, "too long")
	}
	return compile(raw, location)
}

// compile 把文字切成字面量与占位符片段。单个花括号按字面处理；"{{" 后必须紧跟合法名称与 "}}"。
func compile(raw, location string) (template, error) {
	var t template
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t = append(t, segment{kind: literal, text: lit.String()})
			lit.Reset()
		}
	}
	count := 0
	for i := 0; i < len(raw); {
		open := strings.Index(raw[i:], "{{")
		if open < 0 {
			lit.WriteString(raw[i:])
			break
		}
		lit.WriteString(raw[i : i+open])
		i += open + 2
		end := strings.Index(raw[i:], "}}")
		if end < 0 {
			return nil, fail(location, "unclosed placeholder")
		}
		name := raw[i : i+end]
		if name == "code" {
			return nil, fail(location, "{{code}} is placed by the layout")
		}
		kind, ok := placeholderNames[name]
		if !ok {
			return nil, fail(location, "unknown placeholder")
		}
		if count++; count > MaxPlaceholders {
			return nil, fail(location, "too many placeholders")
		}
		flush()
		t = append(t, segment{kind: kind})
		i += end + 2
	}
	flush()
	return t, nil
}

func (t template) render(values *[ttlMinutes + 1]string) string {
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

// Languages 按清单顺序返回语言标签，默认语言在前。
func (r *Renderer) Languages() []string {
	if r == nil {
		return nil
	}
	tags := make([]string, len(r.languages))
	for i, lang := range r.languages {
		tags[i] = lang.tag
	}
	return tags
}

// Multilingual 报告清单是否包含多于一种语言；只有此时才需要读取请求的 Accept-Language。
func (r *Renderer) Multilingual() bool { return r != nil && r.matcher != nil }

type preferenceKey struct{}

// WithLanguagePreference 把请求的语言偏好放入 context，供 Render 选择语言。
func WithLanguagePreference(ctx context.Context, prefs []language.Tag) context.Context {
	if len(prefs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, preferenceKey{}, prefs)
}

// ParseAcceptLanguage 有界解析 Accept-Language。超长、为空或非法时返回 nil，调用方应使用默认语言。
func ParseAcceptLanguage(header string) []language.Tag {
	if header == "" || len(header) > MaxAcceptLanguageBytes {
		return nil
	}
	tags, _, err := language.ParseAcceptLanguage(header)
	if err != nil || len(tags) == 0 {
		return nil
	}
	if len(tags) > MaxAcceptLanguageTags {
		tags = tags[:MaxAcceptLanguageTags]
	}
	return tags
}

// Render 实现 smtp.Renderer。失败一律返回 sender.ErrUnavailable，不记录日志。
func (r *Renderer) Render(ctx context.Context, m sender.CodeMessage) (smtp.EmailContent, error) {
	if r == nil || ctx.Err() != nil {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	index := 0
	if r.matcher != nil {
		if prefs, _ := ctx.Value(preferenceKey{}).([]language.Tag); len(prefs) > 0 {
			// 低置信度的匹配也采用（例如只配了简体时，繁体用户收到简体），只有完全不匹配才回落默认语言。
			// Match 在 No 时仍可能返回非零下标，因此必须检查置信度。
			if _, i, confidence := r.matcher.Match(prefs...); confidence != language.No {
				index = i
			}
		}
	}
	return r.renderIndex(index, m)
}

// RenderIn 用指定语言渲染，供离线预览使用；未知标签返回 sender.ErrUnavailable。
func (r *Renderer) RenderIn(tag string, m sender.CodeMessage) (smtp.EmailContent, error) {
	if r == nil {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	for i, lang := range r.languages {
		if lang.tag == tag {
			return r.renderIndex(i, m)
		}
	}
	return smtp.EmailContent{}, sender.ErrUnavailable
}

func (r *Renderer) renderIndex(index int, m sender.CodeMessage) (smtp.EmailContent, error) {
	if !validMessage(m) || index < 0 || index >= len(r.languages) {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	lang := r.languages[index]
	purpose, ok := lang.purposes[m.Purpose]
	if !ok {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	values := [ttlMinutes + 1]string{
		brand:      lang.brand,
		ttlMinutes: strconv.FormatFloat(math.Ceil(m.TTL.Minutes()), 'f', 0, 64),
	}
	subject := purpose.subject.render(&values)
	text := struct{ heading, intro, codeLabel, expiry, safety, footer string }{
		purpose.heading.render(&values), purpose.intro.render(&values),
		lang.codeLabel.render(&values), lang.expiry.render(&values), lang.safety.render(&values), "",
	}
	if lang.footer != nil {
		text.footer = lang.footer.render(&values)
	}
	// 布局是可信内容；槽位用单次 Replacer 填入，替换值不会再被扫描。填入 HTML 的值整体转义。
	logo := ""
	if lang.logoURL != "" {
		logo = "        <tr>\n          <td class=\"pad\" style=\"padding:32px 40px 8px 40px;\">\n            <img src=\"" + html.EscapeString(lang.logoURL) + "\" alt=\"" + html.EscapeString(lang.brand) + "\" height=\"32\" style=\"display:block;border:0;outline:none;text-decoration:none;height:32px;\">\n          </td>\n        </tr>\n"
	}
	htmlFooter, textFooter := "", ""
	if text.footer != "" {
		htmlFooter = "            <p style=\"margin:0 0 4px 0;\">" + html.EscapeString(text.footer) + "</p>\n"
		textFooter = "\n\n" + text.footer
	}
	page := strings.NewReplacer(
		"{{lang}}", html.EscapeString(lang.tag),
		"{{subject}}", html.EscapeString(subject),
		"{{logo}}", logo,
		"{{brand}}", html.EscapeString(lang.brand),
		"{{heading}}", html.EscapeString(text.heading),
		"{{intro}}", nl2br(html.EscapeString(text.intro)),
		"{{code_label}}", html.EscapeString(text.codeLabel),
		"{{code}}", html.EscapeString(m.Code),
		"{{expiry}}", nl2br(html.EscapeString(text.expiry)),
		"{{safety}}", nl2br(html.EscapeString(text.safety)),
		"{{footer}}", htmlFooter,
	).Replace(layoutHTML)
	plain := strings.NewReplacer(
		"{{heading}}", text.heading,
		"{{intro}}", text.intro,
		"{{code_label}}", text.codeLabel,
		"{{code}}", m.Code,
		"{{expiry}}", text.expiry,
		"{{safety}}", text.safety,
		"{{footer}}", textFooter,
	).Replace(layoutText)
	return smtp.EmailContent{Subject: subject, Text: plain, HTML: page}, nil
}

// nl2br 让文字里的换行在 HTML 中生效；输入已转义，输出的 <br> 是布局的一部分。
func nl2br(escaped string) string { return strings.ReplaceAll(escaped, "\n", "<br>\n") }

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
