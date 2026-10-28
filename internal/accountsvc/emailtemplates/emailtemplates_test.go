package emailtemplates

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

// sentinel 出现在每份测试模板里，用来断言错误信息不回显文件内容。
const sentinel = "SENTINEL-7f3a"

func validEntries() map[string]any {
	m := map[string]any{}
	for _, p := range []string{"SIGN_IN", "BIND", "REAUTH", "SIGN_IN_COMPLETE"} {
		m[p] = map[string]any{
			"subject": "{brand}" + p + "验证码" + sentinel,
			"text":    "【{brand}】" + p + sentinel + "\n\n验证码：{code}\n有效期：{ttl_minutes} 分钟。\n",
		}
	}
	return m
}

func document(templates map[string]any) map[string]any {
	return map[string]any{"version": 1, "templates": templates}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func setField(purpose, field string, value any) func(map[string]any) {
	return func(templates map[string]any) {
		templates[purpose].(map[string]any)[field] = value
	}
}

func mutated(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	templates := validEntries()
	change(templates)
	return encode(t, document(templates))
}

func message(purpose enum.CodePurpose) sender.CodeMessage {
	return sender.CodeMessage{Purpose: purpose, Code: "123456", TTL: 5 * time.Minute}
}

func TestParseAndRenderAllPurposes(t *testing.T) {
	r, err := Parse(encode(t, document(validEntries())), "食方")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		got, err := r.Render(context.Background(), message(p))
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		want := smtp.EmailContent{
			Subject: "食方" + p.String() + "验证码" + sentinel,
			Text:    "【食方】" + p.String() + sentinel + "\n\n验证码：123456\n有效期：5 分钟。\n",
		}
		if got != want {
			t.Fatalf("%v: got %q want %q", p, got, want)
		}
	}
}

func TestRenderTTLRoundsUp(t *testing.T) {
	templates := validEntries()
	templates["SIGN_IN"] = map[string]any{"subject": "登录", "text": "{code} {ttl_minutes}"}
	r, err := Parse(encode(t, document(templates)), "食方")
	if err != nil {
		t.Fatal(err)
	}
	for ttl, want := range map[time.Duration]string{time.Second: "1", 59 * time.Second: "1", time.Minute: "1", 61 * time.Second: "2", 5 * time.Minute: "5", 90 * time.Minute: "90"} {
		got, err := r.Render(context.Background(), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "42", TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "42 "+want {
			t.Errorf("ttl %v: %q", ttl, got.Text)
		}
	}
}

func TestPlaceholderEscapesAndSinglePass(t *testing.T) {
	templates := validEntries()
	templates["SIGN_IN"] = map[string]any{"subject": "{{{brand}}}", "text": "{{code}} = {code}; {{{{"}
	// 品牌本身含占位符形态的文字，渲染后必须保持字面量，不能被二次展开。
	r, err := Parse(encode(t, document(templates)), "{code}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render(context.Background(), message(enum.PurposeSignIn))
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "{{code}}" || got.Text != "{code} = 123456; {{" {
		t.Fatalf("got %q", got)
	}
}

func TestBrandNotRequiredWhenUnused(t *testing.T) {
	templates := validEntries()
	for p := range templates {
		templates[p] = map[string]any{"subject": "验证码", "text": "{code}"}
	}
	if _, err := Parse(encode(t, document(templates)), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(encode(t, document(validEntries())), ""); err == nil || !strings.Contains(err.Error(), "brand") {
		t.Fatalf("{brand} with empty brand: %v", err)
	}
	if _, err := Parse(encode(t, document(validEntries())), "食\n方"); err == nil {
		t.Fatal("invalid brand accepted")
	}
}

func TestParseRejects(t *testing.T) {
	long := func(n int) string { return strings.Repeat("食", n) }
	withRoot := func(change func(map[string]any)) []byte {
		root := document(validEntries())
		change(root)
		return encode(t, root)
	}
	cases := map[string]struct {
		raw      []byte
		location string
	}{
		"empty":               {[]byte(""), "(root)"},
		"not json":            {[]byte("{" + sentinel), "(root)"},
		"array root":          {[]byte(`[]`), "(root)"},
		"trailing":            {append(encode(t, document(validEntries())), []byte(` {}`)...), "(root)"},
		"duplicate key":       {[]byte(`{"version":1,"version":1,"templates":{}}`), "(root)"},
		"invalid utf8":        {append([]byte(`{"x":"`), 0xff, '"', '}'), "(root)"},
		"lone surrogate":      {[]byte(`{"version":1,"templates":{"SIGN_IN":{"subject":"\ud800","text":"{code}"}}}`), "(root)"},
		"unknown root field":  {withRoot(func(m map[string]any) { m[sentinel] = 1 }), "(root)"},
		"missing version":     {withRoot(func(m map[string]any) { delete(m, "version") }), "version"},
		"version 2":           {withRoot(func(m map[string]any) { m["version"] = 2 }), "version"},
		"version string":      {withRoot(func(m map[string]any) { m["version"] = "1" }), "version"},
		"version null":        {withRoot(func(m map[string]any) { m["version"] = nil }), "version"},
		"missing templates":   {withRoot(func(m map[string]any) { delete(m, "templates") }), "templates"},
		"templates array":     {withRoot(func(m map[string]any) { m["templates"] = []any{} }), "templates"},
		"missing purpose":     {mutated(t, func(m map[string]any) { delete(m, "BIND") }), "templates.BIND"},
		"null purpose":        {mutated(t, func(m map[string]any) { m["REAUTH"] = nil }), "templates.REAUTH"},
		"extra purpose":       {mutated(t, func(m map[string]any) { m["UNSPECIFIED"] = m["BIND"] }), "templates"},
		"lowercase purpose":   {mutated(t, func(m map[string]any) { m["sign_in"] = m["SIGN_IN"]; delete(m, "SIGN_IN") }), "templates"},
		"unknown entry field": {mutated(t, setField("SIGN_IN", "html", "<b>{code}</b>")), "templates.SIGN_IN"},
		"missing subject": {mutated(t, func(m map[string]any) {
			delete(m["BIND"].(map[string]any), "subject")
		}), "templates.BIND.subject"},
		"missing text": {mutated(t, func(m map[string]any) {
			delete(m["SIGN_IN_COMPLETE"].(map[string]any), "text")
		}), "templates.SIGN_IN_COMPLETE.text"},
		"null subject":            {mutated(t, setField("SIGN_IN", "subject", nil)), "templates.SIGN_IN.subject"},
		"numeric text":            {mutated(t, setField("SIGN_IN", "text", 1)), "templates.SIGN_IN.text"},
		"subject has code":        {mutated(t, setField("SIGN_IN", "subject", sentinel+"{code}")), "templates.SIGN_IN.subject"},
		"text lacks code":         {mutated(t, setField("REAUTH", "text", sentinel+"{ttl_minutes}")), "templates.REAUTH.text"},
		"unknown placeholder":     {mutated(t, setField("SIGN_IN", "text", "{code}{"+sentinel+"}")), "templates.SIGN_IN.text"},
		"unclosed placeholder":    {mutated(t, setField("SIGN_IN", "text", "{code} {brand"+sentinel)), "templates.SIGN_IN.text"},
		"stray open brace":        {mutated(t, setField("SIGN_IN", "subject", "a { b"+sentinel)), "templates.SIGN_IN.subject"},
		"stray close brace":       {mutated(t, setField("SIGN_IN", "text", "{code} }"+sentinel)), "templates.SIGN_IN.text"},
		"empty placeholder":       {mutated(t, setField("SIGN_IN", "text", "{code}{}"+sentinel)), "templates.SIGN_IN.text"},
		"spaced placeholder":      {mutated(t, setField("SIGN_IN", "text", "{ code }"+sentinel)), "templates.SIGN_IN.text"},
		"subject crlf":            {mutated(t, setField("SIGN_IN", "subject", sentinel+"\r\nBcc: x@example.org")), "templates.SIGN_IN.subject"},
		"subject lf":              {mutated(t, setField("SIGN_IN", "subject", sentinel+"\n")), "templates.SIGN_IN.subject"},
		"subject tab":             {mutated(t, setField("SIGN_IN", "subject", "a\t"+sentinel)), "templates.SIGN_IN.subject"},
		"subject bidi":            {mutated(t, setField("SIGN_IN", "subject", "a‮"+sentinel)), "templates.SIGN_IN.subject"},
		"subject zero width":      {mutated(t, setField("SIGN_IN", "subject", "a​"+sentinel)), "templates.SIGN_IN.subject"},
		"subject line separator":  {mutated(t, setField("SIGN_IN", "subject", "a "+sentinel)), "templates.SIGN_IN.subject"},
		"subject leading space":   {mutated(t, setField("SIGN_IN", "subject", " "+sentinel)), "templates.SIGN_IN.subject"},
		"subject trailing space":  {mutated(t, setField("SIGN_IN", "subject", sentinel+" ")), "templates.SIGN_IN.subject"},
		"subject empty":           {mutated(t, setField("SIGN_IN", "subject", "")), "templates.SIGN_IN.subject"},
		"text cr":                 {mutated(t, setField("SIGN_IN", "text", "{code}\r\n"+sentinel)), "templates.SIGN_IN.text"},
		"text control":            {mutated(t, setField("SIGN_IN", "text", "{code}\x1b"+sentinel)), "templates.SIGN_IN.text"},
		"text bidi":               {mutated(t, setField("SIGN_IN", "text", "{code}‮"+sentinel)), "templates.SIGN_IN.text"},
		"text paragraph sep":      {mutated(t, setField("SIGN_IN", "text", "{code} "+sentinel)), "templates.SIGN_IN.text"},
		"text blank":              {mutated(t, setField("SIGN_IN", "text", " \n\t")), "templates.SIGN_IN.text"},
		"subject 65":              {mutated(t, setField("SIGN_IN", "subject", long(65))), "templates.SIGN_IN.subject"},
		"text 4097":               {mutated(t, setField("SIGN_IN", "text", "{code}"+long(4091))), "templates.SIGN_IN.text"},
		"subject 9 placeholders":  {mutated(t, setField("SIGN_IN", "subject", strings.Repeat("{brand}", 9))), "templates.SIGN_IN.subject"},
		"text 9 placeholders":     {mutated(t, setField("SIGN_IN", "text", "{code}"+strings.Repeat("{ttl_minutes}", 8))), "templates.SIGN_IN.text"},
		"later purpose also read": {mutated(t, setField("SIGN_IN_COMPLETE", "text", sentinel)), "templates.SIGN_IN_COMPLETE.text"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := Parse(c.raw, "食方")
			if err == nil || r != nil {
				t.Fatalf("accepted: %v", r)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, c.location+": ") {
				t.Fatalf("error %q does not start with location %q", msg, c.location)
			}
			if strings.Contains(msg, sentinel) || strings.Contains(msg, "食") || strings.Contains(msg, "Bcc") {
				t.Fatalf("error echoes content: %q", msg)
			}
		})
	}
}

func TestParseBoundaries(t *testing.T) {
	long := func(n int) string { return strings.Repeat("食", n) }
	for name, change := range map[string]func(map[string]any){
		"subject 64":             setField("SIGN_IN", "subject", long(64)),
		"text 4096":              setField("SIGN_IN", "text", "{code}"+long(4090)),
		"subject 8 placeholders": setField("SIGN_IN", "subject", strings.Repeat("{brand}", 8)),
		"text 8 placeholders":    setField("SIGN_IN", "text", "{code}"+strings.Repeat("{ttl_minutes}", 7)),
		"escapes not counted":    setField("SIGN_IN", "text", "{code}"+strings.Repeat("{{}}", 20)),
		"text tab and lf":        setField("SIGN_IN", "text", "\t{code}\n"),
		"subject with ttl":       setField("SIGN_IN", "subject", "{ttl_minutes} 分钟内有效"),
	} {
		if _, err := Parse(mutated(t, change), "食方"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Parse([]byte(strings.Repeat(" ", MaxFileBytes+1)), "食方"); err == nil {
		t.Error("oversized input accepted")
	}
}

func TestRenderRejectsInvalidInput(t *testing.T) {
	r, err := Parse(encode(t, document(validEntries())), "食方")
	if err != nil {
		t.Fatal(err)
	}
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
}

func TestRenderConcurrent(t *testing.T) {
	r, err := Parse(encode(t, document(validEntries())), "食方")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := enum.CodePurpose(i%4 + 1)
			got, err := r.Render(context.Background(), message(p))
			if err != nil || !strings.HasPrefix(got.Subject, "食方"+p.String()) {
				t.Errorf("%v: %v %q", p, err, got.Subject)
			}
		}()
	}
	wg.Wait()
}

// 示例文件须始终能通过当前规则，防止部署文档与解析器漂移。
func TestExampleFile(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/accountsvc/email-templates.example.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(raw, "食方")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw, ""); err == nil {
		t.Fatal("example references {brand} and must require a brand")
	}
	subjects := map[enum.CodePurpose]string{}
	for _, p := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		got, err := r.Render(context.Background(), message(p))
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		if !strings.Contains(got.Text, "123456") || !strings.Contains(got.Text, "5 分钟") || strings.Contains(got.Subject, "123456") {
			t.Fatalf("%v: %q", p, got)
		}
		subjects[p] = got.Subject
	}
	if subjects[enum.PurposeSignInComplete] != "食方登录补全验证码" {
		t.Fatalf("SIGN_IN_COMPLETE subject = %q", subjects[enum.PurposeSignInComplete])
	}
}
