package emailtemplates

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

// sentinel 出现在每份测试清单里，用来断言错误信息不回显文件内容。
const sentinel = "SENTINEL-7f3a"

func languageEntry(brand string) map[string]any {
	templates := map[string]any{}
	for _, p := range []string{"SIGN_IN", "BIND", "REAUTH", "SIGN_IN_COMPLETE"} {
		templates[p] = map[string]any{"subject": "{{brand}} " + p + " " + sentinel, "heading": p + " heading", "intro": p + " intro {{brand}} " + sentinel}
	}
	return map[string]any{
		"brand":     brand,
		"logo_url":  "https://static.example.test/logo.png",
		"common":    map[string]any{"code_label": "code label", "expiry": "expires in {{ttl_minutes}} min", "safety": "safety " + sentinel, "footer": "footer " + sentinel},
		"templates": templates,
	}
}

func manifest() map[string]any {
	return map[string]any{"version": 1, "default_language": "zh-Hans", "languages": map[string]any{
		"zh-Hans": languageEntry("食方"), "zh-Hant": languageEntry("食方繁"), "en": languageEntry("Shifang"),
	}}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutated(t *testing.T, change func(root map[string]any)) []byte {
	t.Helper()
	root := manifest()
	change(root)
	return encode(t, root)
}

func lang(root map[string]any, tag string) map[string]any {
	return root["languages"].(map[string]any)[tag].(map[string]any)
}

func setPurpose(tag, purpose, field string, value any) func(map[string]any) {
	return func(root map[string]any) {
		lang(root, tag)["templates"].(map[string]any)[purpose].(map[string]any)[field] = value
	}
}

func setCommon(tag, field string, value any) func(map[string]any) {
	return func(root map[string]any) { lang(root, tag)["common"].(map[string]any)[field] = value }
}

func message(purpose enum.CodePurpose) sender.CodeMessage {
	return sender.CodeMessage{Purpose: purpose, Code: "123456", TTL: 5 * time.Minute}
}

func mustParse(t *testing.T, raw []byte) *Renderer {
	t.Helper()
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseAndRenderDefaultLanguage(t *testing.T) {
	r := mustParse(t, encode(t, manifest()))
	if got := r.Languages(); strings.Join(got, ",") != "zh-Hans,en,zh-Hant" || !r.Multilingual() {
		t.Fatalf("languages %v multilingual %v", got, r.Multilingual())
	}
	for _, p := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		got, err := r.Render(context.Background(), message(p))
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		if got.Subject != "食方 "+p.String()+" "+sentinel {
			t.Fatalf("%v subject %q", p, got.Subject)
		}
		wantText := p.String() + " heading\n\n" + p.String() + " intro 食方 " + sentinel + "\n\ncode label\n123456\n\nexpires in 5 min\nsafety " + sentinel + "\n\nfooter " + sentinel + "\n"
		if got.Text != wantText {
			t.Fatalf("%v text:\n%q\nwant\n%q", p, got.Text, wantText)
		}
		for _, want := range []string{`<html lang="zh-Hans">`, "<title>食方 " + p.String() + " " + sentinel + "</title>", `src="https://static.example.test/logo.png" alt="食方"`, ">" + p.String() + " heading</h1>", ">123456</p>", ">expires in 5 min</p>", "&copy; 食方</p>", "footer " + sentinel + "</p>"} {
			if !strings.Contains(got.HTML, want) {
				t.Fatalf("%v html lacks %q:\n%s", p, want, got.HTML)
			}
		}
		if strings.Contains(got.HTML, "{{") || strings.Contains(got.Text, "{{") {
			t.Fatalf("%v: unfilled slot:\n%s\n%s", p, got.HTML, got.Text)
		}
	}
}

func TestRenderTTLRoundsUp(t *testing.T) {
	r := mustParse(t, encode(t, manifest()))
	for ttl, want := range map[time.Duration]string{time.Second: "1", 59 * time.Second: "1", time.Minute: "1", 61 * time.Second: "2", 5 * time.Minute: "5", 90 * time.Minute: "90"} {
		got, err := r.Render(context.Background(), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "42", TTL: ttl})
		if err != nil || !strings.Contains(got.Text, "expires in "+want+" min\n") {
			t.Errorf("ttl %v: %v %q", ttl, err, got.Text)
		}
	}
}

func TestHTMLEscapingAndLiteralBraces(t *testing.T) {
	raw := mutated(t, func(root map[string]any) {
		lang(root, "zh-Hans")["brand"] = `A&B <"c">`
		setPurpose("zh-Hans", "SIGN_IN", "intro", "line {one} & {{brand}}\nline }two{ <b>")(root)
	})
	r := mustParse(t, raw)
	got, err := r.Render(context.Background(), message(enum.PurposeSignIn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Subject, `A&B <"c">`) || !strings.Contains(got.Text, `line {one} & A&B <"c">`+"\nline }two{ <b>") {
		t.Fatalf("subject/text must not escape: %q %q", got.Subject, got.Text)
	}
	if !strings.Contains(got.HTML, `line {one} &amp; A&amp;B &lt;&#34;c&#34;&gt;<br>`+"\nline }two{ &lt;b&gt;</p>") || !strings.Contains(got.HTML, `alt="A&amp;B &lt;&#34;c&#34;&gt;"`) || strings.Contains(got.HTML, "<b>") {
		t.Fatalf("html escaping:\n%s", got.HTML)
	}
}

func TestOptionalLogoAndFooter(t *testing.T) {
	raw := mutated(t, func(root map[string]any) {
		delete(lang(root, "zh-Hans"), "logo_url")
		delete(lang(root, "zh-Hans")["common"].(map[string]any), "footer")
	})
	got, err := mustParse(t, raw).Render(context.Background(), message(enum.PurposeBind))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.HTML, "<img") || strings.Contains(got.HTML, "footer "+sentinel) || strings.Contains(got.Text, "footer") {
		t.Fatalf("optional parts rendered:\n%s\n%s", got.HTML, got.Text)
	}
	if !strings.HasSuffix(got.Text, "safety "+sentinel+"\n") || !strings.Contains(got.HTML, "&copy; 食方</p>") {
		t.Fatalf("layout without optional parts:\n%s\n%s", got.HTML, got.Text)
	}
}

func TestLanguageMatching(t *testing.T) {
	r := mustParse(t, encode(t, manifest()))
	for header, wantBrand := range map[string]string{
		"zh-TW": "食方繁", "zh-HK": "食方繁", "zh-MO": "食方繁", "zh-Hant": "食方繁", "zh-Hant-TW;q=0.9, en;q=0.8": "食方繁",
		"zh-CN": "食方", "zh-SG": "食方", "zh": "食方", "zh-Hans": "食方",
		"en-US": "Shifang", "en-GB": "Shifang", "en": "Shifang", "fr, en;q=0.5": "Shifang",
		"ja": "食方", "": "食方", "*": "食方", strings.Repeat("en,", 300): "食方", "not a language!!": "食方",
	} {
		ctx := WithLanguagePreference(context.Background(), ParseAcceptLanguage(header))
		got, err := r.Render(ctx, message(enum.PurposeSignIn))
		if err != nil || !strings.HasPrefix(got.Subject, wantBrand+" ") {
			t.Errorf("Accept-Language %q: brand want %q got subject %q (%v)", header, wantBrand, got.Subject, err)
		}
	}
	if tags := ParseAcceptLanguage(strings.Repeat("en,", 20)); len(tags) != MaxAcceptLanguageTags {
		t.Fatalf("preference cap: %d", len(tags))
	}
	for tag, wantBrand := range map[string]string{"zh-Hans": "食方", "zh-Hant": "食方繁", "en": "Shifang"} {
		if got, err := r.RenderIn(tag, message(enum.PurposeSignIn)); err != nil || !strings.HasPrefix(got.Subject, wantBrand+" ") {
			t.Errorf("RenderIn %s: %v %q", tag, err, got.Subject)
		}
	}
	if _, err := r.RenderIn("fr", message(enum.PurposeSignIn)); !errors.Is(err, sender.ErrUnavailable) {
		t.Fatalf("RenderIn unknown tag: %v", err)
	}
}

// 低置信度的跨文字匹配也采用；只有完全不匹配才回落默认语言。
func TestLanguageMatchingAcrossScripts(t *testing.T) {
	for name, tc := range map[string]struct {
		languages []string // 第一个是默认语言
		header    string
		wantBrand string
	}{
		"hans only, hant request": {[]string{"en", "zh-Hans"}, "zh-TW", "B1"},
		"hant only, hans request": {[]string{"en", "zh-Hant"}, "zh-CN", "B1"},
		"hans only, no match":     {[]string{"en", "zh-Hans"}, "ja", "B0"},
		"both scripts, zh":        {[]string{"en", "zh-Hant", "zh-Hans"}, "zh", "B2"},
	} {
		t.Run(name, func(t *testing.T) {
			raw := mutated(t, func(root map[string]any) {
				langs := map[string]any{}
				for i, tag := range tc.languages {
					langs[tag] = languageEntry("B" + strconv.Itoa(i))
				}
				root["languages"], root["default_language"] = langs, tc.languages[0]
			})
			ctx := WithLanguagePreference(context.Background(), ParseAcceptLanguage(tc.header))
			got, err := mustParse(t, raw).Render(ctx, message(enum.PurposeSignIn))
			if err != nil || !strings.HasPrefix(got.Subject, tc.wantBrand+" ") {
				t.Fatalf("%v %q", err, got.Subject)
			}
		})
	}
}

// 替换值不会再被当作占位符或布局槽位展开。
func TestSinglePassSubstitution(t *testing.T) {
	raw := mutated(t, func(root map[string]any) { lang(root, "zh-Hans")["brand"] = "{{code}}" })
	got, err := mustParse(t, raw).Render(context.Background(), message(enum.PurposeSignIn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Subject, "{{code}} ") || !strings.Contains(got.Text, "intro {{code}} ") || !strings.Contains(got.HTML, "intro {{code}} ") {
		t.Fatalf("brand must stay literal: %q %q", got.Subject, got.Text)
	}
	if strings.Count(got.HTML, "123456") != 1 || !strings.Contains(got.HTML, ">123456</p>") || strings.Count(got.Text, "123456") != 1 {
		t.Fatalf("code expanded more than once:\n%s\n%s", got.HTML, got.Text)
	}
}

var update = flag.Bool("update", false, "rewrite golden files")

// 黄金文件固定内置布局的 HTML 输出，防止无意改动版式。
func TestLayoutGolden(t *testing.T) {
	full := mutated(t, func(root map[string]any) {
		lang(root, "zh-Hans")["brand"] = "案例 & <Co>"
		setPurpose("zh-Hans", "SIGN_IN", "intro", "第一行\n第二行 {{brand}}")(root)
	})
	minimal := mutated(t, func(root map[string]any) {
		delete(lang(root, "zh-Hans"), "logo_url")
		delete(lang(root, "zh-Hans")["common"].(map[string]any), "footer")
	})
	for name, raw := range map[string][]byte{"full": full, "minimal": minimal} {
		got, err := mustParse(t, raw).Render(context.Background(), message(enum.PurposeSignIn))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testdata", name+".golden.html")
		if *update {
			if err := os.WriteFile(path, []byte(got.HTML), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to create)", name, err)
		}
		if string(want) != got.HTML {
			t.Errorf("%s: HTML differs from %s (run with -update after intentional layout changes)", name, path)
		}
	}
}

func TestSingleLanguageIgnoresPreference(t *testing.T) {
	raw := mutated(t, func(root map[string]any) {
		root["languages"] = map[string]any{"en": languageEntry("Shifang")}
		root["default_language"] = "en"
	})
	r := mustParse(t, raw)
	if r.Multilingual() {
		t.Fatal("single language reported as multilingual")
	}
	ctx := WithLanguagePreference(context.Background(), ParseAcceptLanguage("zh-TW"))
	if got, err := r.Render(ctx, message(enum.PurposeSignIn)); err != nil || !strings.HasPrefix(got.Subject, "Shifang ") {
		t.Fatalf("%v %q", err, got.Subject)
	}
}

func TestParseRejects(t *testing.T) {
	long := func(n int) string { return strings.Repeat("食", n) }
	cases := map[string]struct {
		raw      []byte
		location string
	}{
		"empty":           {[]byte(""), "(root)"},
		"not json":        {[]byte("{" + sentinel), "(root)"},
		"array root":      {[]byte(`[]`), "(root)"},
		"trailing":        {append(encode(t, manifest()), []byte(` {}`)...), "(root)"},
		"duplicate key":   {[]byte(`{"version":1,"version":1,"default_language":"en","languages":{}}`), "(root)"},
		"invalid utf8":    {append([]byte(`{"x":"`), 0xff, '"', '}'), "(root)"},
		"unknown root":    {mutated(t, func(m map[string]any) { m[sentinel] = 1 }), "(root)"},
		"missing version": {mutated(t, func(m map[string]any) { delete(m, "version") }), "version"},
		"version 2":       {mutated(t, func(m map[string]any) { m["version"] = 2 }), "version"},
		"version string":  {mutated(t, func(m map[string]any) { m["version"] = "1" }), "version"},
		"no languages":    {mutated(t, func(m map[string]any) { m["languages"] = map[string]any{} }), "languages"},
		"languages array": {mutated(t, func(m map[string]any) { m["languages"] = []any{} }), "languages"},
		"nine languages": {mutated(t, func(m map[string]any) {
			for _, tag := range []string{"fr", "de", "es", "it", "pt", "ko"} {
				m["languages"].(map[string]any)[tag] = languageEntry("x")
			}
		}), "languages"},
		"default missing":   {mutated(t, func(m map[string]any) { delete(m, "default_language") }), "default_language"},
		"default unknown":   {mutated(t, func(m map[string]any) { m["default_language"] = "fr" }), "default_language"},
		"default null":      {mutated(t, func(m map[string]any) { m["default_language"] = nil }), "default_language"},
		"tag not canonical": {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)["zh-hant"] = languageEntry("x") }), "languages"},
		"tag und":           {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)["und"] = languageEntry("x") }), "languages"},
		"tag garbage":       {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)[sentinel] = languageEntry("x") }), "languages"},
		"tag private":       {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)["x-foo"] = languageEntry("x") }), "languages"},
		"tag private ext":   {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)["en-x-foo"] = languageEntry("x") }), "languages"},
		"language null":     {mutated(t, func(m map[string]any) { m["languages"].(map[string]any)["en"] = nil }), "languages.en"},
		"language unknown":  {mutated(t, func(m map[string]any) { lang(m, "en")[sentinel] = 1 }), "languages.en"},
		"brand missing":     {mutated(t, func(m map[string]any) { delete(lang(m, "en"), "brand") }), "languages.en.brand"},
		"brand empty":       {mutated(t, func(m map[string]any) { lang(m, "en")["brand"] = "" }), "languages.en.brand"},
		"brand newline":     {mutated(t, func(m map[string]any) { lang(m, "en")["brand"] = "a\nb" + sentinel }), "languages.en.brand"},
		"brand long":        {mutated(t, func(m map[string]any) { lang(m, "en")["brand"] = long(33) }), "languages.en.brand"},
		"logo http":         {mutated(t, func(m map[string]any) { lang(m, "en")["logo_url"] = "http://x.example/" + sentinel }), "languages.en.logo_url"},
		"logo userinfo":     {mutated(t, func(m map[string]any) { lang(m, "en")["logo_url"] = "https://u:p@x.example/l.png" }), "languages.en.logo_url"},
		"logo space":        {mutated(t, func(m map[string]any) { lang(m, "en")["logo_url"] = "https://x.example/a b.png" }), "languages.en.logo_url"},
		"logo data":         {mutated(t, func(m map[string]any) { lang(m, "en")["logo_url"] = "data:image/png;base64,AAAA" }), "languages.en.logo_url"},
		"logo null":         {mutated(t, func(m map[string]any) { lang(m, "en")["logo_url"] = nil }), "languages.en.logo_url"},
		"logo long": {mutated(t, func(m map[string]any) {
			lang(m, "en")["logo_url"] = "https://x.example/" + strings.Repeat("a", MaxLogoURLBytes)
		}), "languages.en.logo_url"},
		"common missing":       {mutated(t, func(m map[string]any) { delete(lang(m, "en"), "common") }), "languages.en.common"},
		"common unknown":       {mutated(t, setCommon("en", sentinel, "x")), "languages.en.common"},
		"common missing field": {mutated(t, func(m map[string]any) { delete(lang(m, "en")["common"].(map[string]any), "safety") }), "languages.en.common.safety"},
		"common null footer":   {mutated(t, setCommon("en", "footer", nil)), "languages.en.common.footer"},
		"common code":          {mutated(t, setCommon("en", "expiry", "{{code}} "+sentinel)), "languages.en.common.expiry"},
		"common cr":            {mutated(t, setCommon("en", "safety", "a\r\nb"+sentinel)), "languages.en.common.safety"},
		"code_label 65":        {mutated(t, setCommon("en", "code_label", long(65))), "languages.en.common.code_label"},
		"expiry 1025":          {mutated(t, setCommon("en", "expiry", long(1025))), "languages.en.common.expiry"},
		"templates missing":    {mutated(t, func(m map[string]any) { delete(lang(m, "en"), "templates") }), "languages.en.templates"},
		"purpose missing":      {mutated(t, func(m map[string]any) { delete(lang(m, "en")["templates"].(map[string]any), "BIND") }), "languages.en.templates.BIND"},
		"purpose extra": {mutated(t, func(m map[string]any) {
			tpl := lang(m, "en")["templates"].(map[string]any)
			tpl["UNSPECIFIED"] = tpl["BIND"]
		}), "languages.en.templates"},
		"purpose lowercase": {mutated(t, func(m map[string]any) {
			tpl := lang(m, "en")["templates"].(map[string]any)
			tpl["sign_in"] = tpl["SIGN_IN"]
			delete(tpl, "SIGN_IN")
		}), "languages.en.templates"},
		"purpose unknown field": {mutated(t, setPurpose("en", "SIGN_IN", "html_file", "x.html")), "languages.en.templates.SIGN_IN"},
		"subject missing": {mutated(t, func(m map[string]any) {
			delete(lang(m, "en")["templates"].(map[string]any)["REAUTH"].(map[string]any), "subject")
		}), "languages.en.templates.REAUTH.subject"},
		"subject code":           {mutated(t, setPurpose("en", "SIGN_IN", "subject", "{{code}} "+sentinel)), "languages.en.templates.SIGN_IN.subject"},
		"subject unknown":        {mutated(t, setPurpose("en", "SIGN_IN", "subject", "{{"+sentinel+"}}")), "languages.en.templates.SIGN_IN.subject"},
		"subject unclosed":       {mutated(t, setPurpose("en", "SIGN_IN", "subject", "{{brand "+sentinel)), "languages.en.templates.SIGN_IN.subject"},
		"subject spaced":         {mutated(t, setPurpose("en", "SIGN_IN", "subject", "{{ brand }}")), "languages.en.templates.SIGN_IN.subject"},
		"subject empty name":     {mutated(t, setPurpose("en", "SIGN_IN", "subject", "{{}}"+sentinel)), "languages.en.templates.SIGN_IN.subject"},
		"subject crlf":           {mutated(t, setPurpose("en", "SIGN_IN", "subject", sentinel+"\r\nBcc: x@example.org")), "languages.en.templates.SIGN_IN.subject"},
		"subject tab":            {mutated(t, setPurpose("en", "SIGN_IN", "subject", "a\t"+sentinel)), "languages.en.templates.SIGN_IN.subject"},
		"subject bidi":           {mutated(t, setPurpose("en", "SIGN_IN", "subject", "a\u202e"+sentinel)), "languages.en.templates.SIGN_IN.subject"},
		"subject trailing space": {mutated(t, setPurpose("en", "SIGN_IN", "subject", sentinel+" ")), "languages.en.templates.SIGN_IN.subject"},
		"subject 65":             {mutated(t, setPurpose("en", "SIGN_IN", "subject", long(65))), "languages.en.templates.SIGN_IN.subject"},
		"intro 9 placeholders":   {mutated(t, setPurpose("en", "SIGN_IN", "intro", strings.Repeat("{{brand}}", 9))), "languages.en.templates.SIGN_IN.intro"},
		"heading number":         {mutated(t, setPurpose("en", "SIGN_IN", "heading", 1)), "languages.en.templates.SIGN_IN.heading"},
		"heading blank":          {mutated(t, setPurpose("en", "SIGN_IN", "heading", " \n")), "languages.en.templates.SIGN_IN.heading"},
		"intro control":          {mutated(t, setPurpose("en", "SIGN_IN", "intro", "a\x1bb"+sentinel)), "languages.en.templates.SIGN_IN.intro"},
		"intro 1025":             {mutated(t, setPurpose("en", "SIGN_IN", "intro", long(1025))), "languages.en.templates.SIGN_IN.intro"},
		"later language checked": {mutated(t, setPurpose("zh-Hant", "SIGN_IN_COMPLETE", "intro", "{{code}}")), "languages.zh-Hant.templates.SIGN_IN_COMPLETE.intro"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := Parse(c.raw)
			if err == nil || r != nil {
				t.Fatalf("accepted: %v", r)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, c.location+": ") {
				t.Fatalf("error %q does not start with location %q", msg, c.location)
			}
			if strings.Contains(msg, sentinel) || strings.Contains(msg, "食") || strings.Contains(msg, "Bcc") || strings.Contains(msg, "example") {
				t.Fatalf("error echoes content: %q", msg)
			}
		})
	}
}

func TestParseBoundaries(t *testing.T) {
	long := func(n int) string { return strings.Repeat("食", n) }
	for name, change := range map[string]func(map[string]any){
		"subject 64":             setPurpose("en", "SIGN_IN", "subject", long(64)),
		"subject 7 placeholders": setPurpose("en", "SIGN_IN", "subject", strings.Repeat("{{brand}}", 7)),
		"intro 8 placeholders":   setPurpose("en", "SIGN_IN", "intro", strings.Repeat("{{brand}}", 8)),
		"heading 64":             setPurpose("en", "SIGN_IN", "heading", long(64)),
		"intro 1024":             setPurpose("en", "SIGN_IN", "intro", long(1024)),
		"intro tab and lf":       setPurpose("en", "SIGN_IN", "intro", "\ta\nb\n"),
		"single braces":          setPurpose("en", "SIGN_IN", "intro", "a { b } c }} d"),
		"eight languages": func(m map[string]any) {
			for _, tag := range []string{"fr", "de", "es", "it", "pt"} {
				m["languages"].(map[string]any)[tag] = languageEntry("x")
			}
		},
		"logo 2048": func(m map[string]any) {
			lang(m, "en")["logo_url"] = "https://x.example/" + strings.Repeat("a", MaxLogoURLBytes-len("https://x.example/"))
		},
	} {
		if _, err := Parse(mutated(t, change)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Parse([]byte(strings.Repeat(" ", MaxFileBytes+1))); err == nil {
		t.Error("oversized input accepted")
	}
}

func TestRenderRejectsInvalidInput(t *testing.T) {
	r := mustParse(t, encode(t, manifest()))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Render(canceled, message(enum.PurposeSignIn)); !errors.Is(err, sender.ErrUnavailable) {
		t.Errorf("canceled: %v", err)
	}
	for name, m := range map[string]sender.CodeMessage{
		"unspecified": {Purpose: 0, Code: "1", TTL: time.Minute},
		"unknown":     {Purpose: 99, Code: "1", TTL: time.Minute},
		"empty code":  {Purpose: enum.PurposeSignIn, TTL: time.Minute},
		"non-digit":   {Purpose: enum.PurposeSignIn, Code: "12a", TTL: time.Minute},
		"html code":   {Purpose: enum.PurposeSignIn, Code: "<b>", TTL: time.Minute},
		"zero ttl":    {Purpose: enum.PurposeSignIn, Code: "1"},
	} {
		if got, err := r.Render(context.Background(), m); !errors.Is(err, sender.ErrUnavailable) || got != (smtp.EmailContent{}) {
			t.Errorf("%s: %v %q", name, err, got)
		}
	}
	var zero Renderer
	if _, err := zero.Render(context.Background(), message(enum.PurposeSignIn)); !errors.Is(err, sender.ErrUnavailable) {
		t.Errorf("zero value: %v", err)
	}
	var nilRenderer *Renderer
	if _, err := nilRenderer.Render(context.Background(), message(enum.PurposeSignIn)); !errors.Is(err, sender.ErrUnavailable) {
		t.Errorf("nil: %v", err)
	}
	if nilRenderer.Multilingual() || nilRenderer.Languages() != nil {
		t.Error("nil renderer metadata")
	}
}

func TestRenderConcurrent(t *testing.T) {
	r := mustParse(t, encode(t, manifest()))
	headers := []string{"zh-TW", "en", "", "ja"}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := enum.CodePurpose(i%4 + 1)
			ctx := WithLanguagePreference(context.Background(), ParseAcceptLanguage(headers[i%len(headers)]))
			got, err := r.Render(ctx, message(p))
			if err != nil || !strings.Contains(got.Subject, p.String()) || !strings.Contains(got.HTML, ">123456</p>") {
				t.Errorf("%v: %v %q", p, err, got.Subject)
			}
		}()
	}
	wg.Wait()
}

// 样例清单须始终能通过当前规则，防止部署文档与解析器漂移。
func TestExampleManifest(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/accountsvc/email-templates.example.json")
	if err != nil {
		t.Fatal(err)
	}
	r := mustParse(t, raw)
	if strings.Join(r.Languages(), ",") != "zh-Hans,en,zh-Hant" {
		t.Fatalf("languages %v", r.Languages())
	}
	for header, wantSubject := range map[string]string{"zh-CN": "案例登录补全验证码", "zh-TW": "案例登入補全驗證碼", "en-GB": "Finish signing in to Example"} {
		ctx := WithLanguagePreference(context.Background(), ParseAcceptLanguage(header))
		got, err := r.Render(ctx, message(enum.PurposeSignInComplete))
		if err != nil || got.Subject != wantSubject || !strings.Contains(got.Text, "123456") || !strings.Contains(got.HTML, ">123456</p>") || strings.Contains(got.Subject, "123456") {
			t.Fatalf("%s: %v %q", header, err, got.Subject)
		}
	}
}
