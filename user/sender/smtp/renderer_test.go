package smtp_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	delivery "github.com/bbxx111/accountkit/user/sender/smtp"
)

type renderFunc func(context.Context, sender.CodeMessage) (delivery.EmailContent, error)

func (f renderFunc) Render(ctx context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
	return f(ctx, m)
}

type nilRenderer struct{}

func (*nilRenderer) Render(context.Context, sender.CodeMessage) (delivery.EmailContent, error) {
	panic("nil renderer called")
}
func validCodeMessage() sender.CodeMessage {
	return sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}
}
func assertNoSMTPConnection(t *testing.T, f *smtpFixture) {
	t.Helper()
	// Closing the listener joins the accept loop; a queued accidental connection is counted.
	_ = f.ln.Close()
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connections != 0 {
		t.Fatal("invalid or canceled render reached SMTP")
	}
}

func TestSMTPRendererConstruction(t *testing.T) {
	f := newSMTPFixture(t, "implicit", nil)
	var calls atomic.Int32
	renderer := renderFunc(func(context.Context, sender.CodeMessage) (delivery.EmailContent, error) {
		calls.Add(1)
		return delivery.EmailContent{}, nil
	})
	if _, err := delivery.NewWithRenderer(f.config(), renderer); err != nil {
		t.Fatal(err)
	}
	var pointer *nilRenderer
	var function renderFunc
	for _, r := range []delivery.Renderer{nil, pointer, function} {
		if _, err := delivery.NewWithRenderer(f.config(), r); err == nil || strings.Contains(err.Error(), "123456") {
			t.Fatal("missing renderer accepted or unsafe error")
		}
	}
	cfg := f.config()
	cfg.Port = 0
	if _, err := delivery.NewWithRenderer(cfg, renderer); err == nil {
		t.Fatal("new constructor bypassed connection config validation")
	}
	if calls.Load() != 0 {
		t.Fatal("constructor rendered")
	}
	assertNoSMTPConnection(t, f)
}

func TestSMTPRendererMIME(t *testing.T) {
	for _, html := range []string{"", "<p>Use <strong>123456</strong> &amp; voilà</p>"} {
		t.Run(map[bool]string{false: "plain", true: "alternative"}[html != ""], func(t *testing.T) {
			f := newSMTPFixture(t, "implicit", nil)
			want := delivery.EmailContent{Subject: "Acme — vérification ✓", Text: "Acme verification 123456\nExpires in 1 minute.\n", HTML: html}
			var calls int
			s, err := delivery.NewWithRenderer(f.config(), renderFunc(func(_ context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
				calls++
				if m != validCodeMessage() {
					t.Error("message altered")
				}
				return want, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SendEmail(context.Background(), "person@example.org", validCodeMessage()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			raw := string(f.data)
			accepted := f.accepted
			f.mu.Unlock()
			if calls != 1 || accepted != 1 {
				t.Fatal("render or submission repeated")
			}
			msg, err := mail.ReadMessage(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
			if err != nil || subject != want.Subject {
				t.Fatal("subject failed to decode", subject, err)
			}
			if msg.Header.Get("From") != "sender@example.org" || msg.Header.Get("To") != "person@example.org" {
				t.Fatal("renderer changed envelope headers")
			}
			media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			if html == "" {
				if media != "text/plain" || strings.ToUpper(params["charset"]) != "UTF-8" || msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
					t.Fatal("invalid plain MIME")
				}
				body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
				if err != nil {
					t.Fatal(err)
				}
				if strings.ReplaceAll(string(body), "\r\n", "\n") != want.Text {
					t.Fatal("custom text changed or fixed text appended")
				}
			} else {
				if media != "multipart/alternative" {
					t.Fatal("missing multipart alternative")
				}
				reader := multipart.NewReader(msg.Body, params["boundary"])
				for i, content := range []string{want.Text, want.HTML} {
					part, err := reader.NextRawPart()
					if err != nil {
						t.Fatal(err)
					}
					kind, p, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
					if err != nil {
						t.Fatal(err)
					}
					if kind != []string{"text/plain", "text/html"}[i] || strings.ToUpper(p["charset"]) != "UTF-8" || part.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
						t.Fatal("invalid or out of order alternative")
					}
					body, err := io.ReadAll(quotedprintable.NewReader(part))
					if err != nil {
						t.Fatal(err)
					}
					if strings.ReplaceAll(string(body), "\r\n", "\n") != content {
						t.Fatal("alternative did not preserve custom content")
					}
				}
				if _, err = reader.NextRawPart(); err != io.EOF {
					t.Fatal("unexpected MIME part", err)
				}
			}
		})
	}
}

func TestSMTPRendererRejectsInputBeforeRendering(t *testing.T) {
	f := newSMTPFixture(t, "implicit", nil)
	var calls atomic.Int32
	s, err := delivery.NewWithRenderer(f.config(), renderFunc(func(context.Context, sender.CodeMessage) (delivery.EmailContent, error) {
		calls.Add(1)
		return delivery.EmailContent{Subject: "unused", Text: "unused"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		to string
		m  sender.CodeMessage
	}{
		{"invalid", validCodeMessage()},
		{"person@example.org\r\nBcc: other@example.org", validCodeMessage()},
		{"person@example.org", sender.CodeMessage{Purpose: enum.CodePurpose(99), Code: "123456", TTL: time.Minute}},
		{"person@example.org", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "", TTL: time.Minute}},
		{"person@example.org", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "１２３４５６", TTL: time.Minute}},
		{"person@example.org", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "12a456", TTL: time.Minute}},
		{"person@example.org", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "123456", TTL: 0}},
		{"person@example.org", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "123456", TTL: -time.Second}},
	}
	for _, tc := range cases {
		if err = s.SendEmail(context.Background(), tc.to, tc.m); err != sender.ErrUnavailable {
			t.Fatal("invalid input not safely rejected")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input reached renderer")
	}
	assertNoSMTPConnection(t, f)
}

func TestSMTPRendererFailureIsSafe(t *testing.T) {
	private := errors.New("person@example.org 123456 body private-context")
	for _, tc := range []struct {
		name    string
		content delivery.EmailContent
		err     error
	}{
		{"render error", delivery.EmailContent{Subject: "unused", Text: "unused"}, private},
		{"CR", delivery.EmailContent{Subject: "a\rBcc: other", Text: "unused"}, nil},
		{"LF", delivery.EmailContent{Subject: "a\nBcc: other", Text: "unused"}, nil},
		{"blank subject", delivery.EmailContent{Subject: " \t", Text: "unused"}, nil},
		{"blank text", delivery.EmailContent{Subject: "unused", Text: " \n\t", HTML: "<p>123456</p>"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSMTPFixture(t, "implicit", nil)
			var calls int
			s, err := delivery.NewWithRenderer(f.config(), renderFunc(func(context.Context, sender.CodeMessage) (delivery.EmailContent, error) {
				calls++
				return tc.content, tc.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SendEmail(context.Background(), "person@example.org", validCodeMessage())
			if err != sender.ErrUnavailable || errors.Is(err, private) || calls != 1 {
				t.Fatal("render failure wrapped, retried, or not safely converted")
			}
			assertNoSMTPConnection(t, f)
		})
	}
}

type languageKey struct{}

func TestSMTPRendererContextAndConcurrency(t *testing.T) {
	f := newSMTPFixture(t, "implicit", func(f *smtpFixture) { f.serveCount = 2 })
	s, err := delivery.NewWithRenderer(f.config(), renderFunc(func(ctx context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
		language, ok := ctx.Value(languageKey{}).(string)
		if !ok || m != validCodeMessage() {
			t.Error("context or message lost")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing total deadline")
		}
		switch language {
		case "zh":
			return delivery.EmailContent{Subject: "宿主验证码", Text: "使用 123456，一分钟内有效。"}, nil
		case "en":
			return delivery.EmailContent{Subject: "Host verification", Text: "Use 123456 within one minute."}, nil
		default:
			t.Error("request language contaminated")
			return delivery.EmailContent{}, errors.New("unsupported language")
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, language := range []string{"zh", "en"} {
		wg.Go(func() {
			if err := s.SendEmail(context.WithValue(context.Background(), languageKey{}, language), "person@example.org", validCodeMessage()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) != 2 || f.accepted != 2 {
		t.Fatal("concurrent requests lost or repeated")
	}
	seen := map[string]string{}
	for _, raw := range f.messages {
		msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if err != nil {
			t.Fatal(err)
		}
		seen[subject] = strings.TrimSuffix(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	}
	if seen["宿主验证码"] != "使用 123456，一分钟内有效。" || seen["Host verification"] != "Use 123456 within one minute." {
		t.Fatal("concurrent language contents crossed requests")
	}
}

func TestSMTPRendererCancellation(t *testing.T) {
	for _, stage := range []string{"pre-canceled", "render timeout", "canceled before return"} {
		t.Run(stage, func(t *testing.T) {
			f := newSMTPFixture(t, "implicit", nil)
			cfg := f.config()
			cfg.Timeout = 50 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s, err := delivery.NewWithRenderer(cfg, renderFunc(func(ctx context.Context, _ sender.CodeMessage) (delivery.EmailContent, error) {
				calls++
				if stage == "render timeout" {
					<-ctx.Done()
				}
				if stage == "canceled before return" {
					cancel()
				}
				return delivery.EmailContent{Subject: "unused", Text: "unused"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if stage == "pre-canceled" {
				cancel()
			}
			start := time.Now()
			if err = s.SendEmail(ctx, "person@example.org", validCodeMessage()); err != sender.ErrUnavailable {
				t.Fatal("cancellation must be safe")
			}
			if stage == "pre-canceled" && calls != 0 {
				t.Fatal("canceled request rendered")
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("cooperative render timeout not bounded")
			}
			assertNoSMTPConnection(t, f)
		})
	}
}

func TestSMTPRendererSharesDeliveryBudget(t *testing.T) {
	f := newSMTPFixture(t, "implicit", func(f *smtpFixture) { f.stall = "greeting" })
	cfg := f.config()
	cfg.Timeout = 400 * time.Millisecond
	var deadline time.Time
	s, err := delivery.NewWithRenderer(cfg, renderFunc(func(ctx context.Context, _ sender.CodeMessage) (delivery.EmailContent, error) {
		deadline, _ = ctx.Deadline()
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		return delivery.EmailContent{Subject: "Acme", Text: "Use 123456"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SendEmail(context.Background(), "person@example.org", validCodeMessage()); err != sender.ErrUnavailable {
		t.Fatal("stalled delivery must fail")
	}
	if deadline.IsZero() || time.Since(deadline) > 150*time.Millisecond {
		t.Fatal("network reset the render deadline")
	}
}

func TestLegacyChineseRendererShortTTL(t *testing.T) {
	content, err := (delivery.LegacyChineseRenderer{}).Render(context.Background(), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Nanosecond})
	if err != nil || !strings.Contains(content.Text, "有效期：1 分钟。") || content.HTML != "" {
		t.Fatal("positive short legacy TTL rounded down")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = (delivery.LegacyChineseRenderer{}).Render(ctx, validCodeMessage()); err != sender.ErrUnavailable {
		t.Fatal("legacy renderer ignored cancellation")
	}
	if _, err = (delivery.LegacyChineseRenderer{}).Render(context.Background(), sender.CodeMessage{}); err != sender.ErrUnavailable {
		t.Fatal("legacy renderer accepted invalid code message")
	}
}

func TestSMTPRendererFoldsLongSubject(t *testing.T) {
	for _, tc := range []struct{ name, subject string }{
		{"unicode", strings.Repeat("验", 100)},
		{"four-byte Unicode", strings.Repeat("界🦊", 100)},
		{"ASCII words", strings.Repeat("verification ", 100)},
		{"ASCII long token", strings.Repeat("x", 1200)},
		{"exact whitespace", " \t" + strings.Repeat("Acme  验证\t  confirmation ", 60) + " \t "},
		{"ASCII whitespace", " \t" + strings.Repeat("Acme  \t ", 180) + " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSMTPFixture(t, "implicit", nil)
			s, err := delivery.NewWithRenderer(f.config(), renderFunc(func(context.Context, sender.CodeMessage) (delivery.EmailContent, error) {
				return delivery.EmailContent{Subject: tc.subject, Text: "Use 123456"}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SendEmail(context.Background(), "person@example.org", validCodeMessage()); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			raw := string(f.data)
			accepted := f.accepted
			f.mu.Unlock()
			if accepted != 1 {
				t.Fatal("long subject was not submitted exactly once")
			}
			headers, _, ok := strings.Cut(strings.ReplaceAll(raw, "\r\n", "\n"), "\n\n")
			if !ok {
				t.Fatal("missing header/body separator")
			}
			for _, line := range strings.Split(headers, "\n") {
				if len(line) > 998 {
					t.Errorf("physical header line has %d octets, exceeds 998", len(line))
				}
			}
			msg, err := mail.ReadMessage(strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			subject := msg.Header.Get("Subject")
			for _, word := range strings.Fields(subject) {
				if !strings.HasPrefix(word, "=?") {
					continue
				}
				decoded, err := new(mime.WordDecoder).Decode(word)
				if len(word) > 75 || err != nil || !utf8.ValidString(decoded) {
					t.Error("invalid encoded-word or split UTF-8 character")
				}
			}
			decoded, err := new(mime.WordDecoder).DecodeHeader(subject)
			if err != nil || decoded != tc.subject {
				t.Error("folding changed subject text or whitespace", err)
			}
		})
	}
}
