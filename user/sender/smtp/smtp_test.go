package smtp_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	delivery "github.com/bbxx111/accountkit/user/sender/smtp"
)

// smtpFixture 使用真实 TCP/TLS 和 SMTP 对话，验证凭据与正文只在加密后发送。
type smtpFixture struct {
	ln          net.Listener
	tls         *tls.Config
	roots       *x509.CertPool
	mode        string
	reject      string
	noStartTLS  bool
	badQuit     bool
	stall       string
	stallTLS    bool
	mu          sync.Mutex
	commands    []string
	data        []byte
	auth        string
	clearSecret bool
	accepted    int
	connections int
	done        chan struct{}
}

func newSMTPFixture(t *testing.T, mode string, configure func(*smtpFixture)) *smtpFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "SMTP test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &smtpFixture{ln: ln, roots: roots, tls: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, mode: mode, done: make(chan struct{})}
	if configure != nil {
		configure(f)
	}
	go f.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-f.done:
		case <-time.After(3 * time.Second):
			t.Error("SMTP fixture did not stop")
		}
	})
	return f
}

func (f *smtpFixture) config() delivery.Config {
	_, p, _ := net.SplitHostPort(f.ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return delivery.Config{Host: "127.0.0.1", Port: port, From: "sender@example.org", Username: "test-user", Password: "synthetic-password", TLSMode: f.mode, Timeout: time.Second, TLSConfig: &tls.Config{RootCAs: f.roots}}
}

func (f *smtpFixture) serve() {
	defer close(f.done)
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	f.mu.Lock()
	f.connections++
	f.mu.Unlock()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	encrypted := false
	if f.mode == "implicit" {
		if f.stallTLS {
			var buf [4096]byte
			for {
				if _, err := conn.Read(buf[:]); err != nil {
					return
				}
			}
		}
		secured := tls.Server(conn, f.tls)
		if secured.Handshake() != nil {
			return
		}
		conn = secured
		encrypted = true
	}
	tp := textproto.NewConn(conn)
	defer tp.Close()
	if f.stall == "greeting" {
		var buf [1]byte
		_, _ = conn.Read(buf[:])
		return
	}
	if tp.PrintfLine("220 fixture SMTP") != nil {
		return
	}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		command := strings.SplitN(line, " ", 2)[0]
		f.mu.Lock()
		f.commands = append(f.commands, command)
		f.mu.Unlock()
		if f.stall == command {
			var buf [1]byte
			_, _ = conn.Read(buf[:])
			return
		}
		switch command {
		case "EHLO":
			_ = tp.PrintfLine("250-fixture")
			if !encrypted && !f.noStartTLS && f.mode == "starttls" {
				_ = tp.PrintfLine("250-STARTTLS")
			}
			_ = tp.PrintfLine("250 AUTH PLAIN")
		case "HELO":
			_ = tp.PrintfLine("250 fixture")
		case "STARTTLS":
			_ = tp.PrintfLine("220 begin TLS")
			if f.stallTLS {
				var buf [4096]byte
				for {
					if _, err := conn.Read(buf[:]); err != nil {
						return
					}
				}
			}
			secured := tls.Server(conn, f.tls)
			if secured.Handshake() != nil {
				return
			}
			conn = secured
			tp = textproto.NewConn(conn)
			encrypted = true
		case "AUTH":
			parts := strings.Split(line, " ")
			if len(parts) != 3 {
				return
			}
			decoded, _ := base64.StdEncoding.DecodeString(parts[2])
			f.mu.Lock()
			f.auth = string(decoded)
			if !encrypted {
				f.clearSecret = true
			}
			f.mu.Unlock()
			if f.reject == "AUTH" {
				_ = tp.PrintfLine("535 person@example.org synthetic-password 123456")
				return
			}
			_ = tp.PrintfLine("235 authenticated")
		case "MAIL":
			if f.reject == "MAIL" {
				_ = tp.PrintfLine("550 person@example.org rejected")
			} else {
				_ = tp.PrintfLine("250 sender accepted")
			}
		case "RCPT":
			if f.reject == "RCPT" {
				_ = tp.PrintfLine("550 person@example.org rejected")
			} else {
				_ = tp.PrintfLine("250 recipient accepted")
			}
		case "DATA":
			_ = tp.PrintfLine("354 send message")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = data
			if !encrypted {
				f.clearSecret = true
			}
			if f.reject != "DATA" {
				f.accepted++
			}
			f.mu.Unlock()
			if f.reject == "DATA" {
				_ = tp.PrintfLine("554 person@example.org body 123456 rejected")
			} else {
				_ = tp.PrintfLine("250 queued")
			}
		case "QUIT":
			if !f.badQuit {
				_ = tp.PrintfLine("221 bye")
			}
			return
		default:
			_ = tp.PrintfLine("500 unknown command")
		}
	}
}

func TestSMTPTemplatesAndEncryptedAuth(t *testing.T) {
	for _, mode := range []string{"implicit", "starttls"} {
		for _, tc := range []struct {
			purpose enum.CodePurpose
			word    string
		}{{enum.PurposeSignIn, "登录"}, {enum.PurposeBind, "绑定"}, {enum.PurposeReauth, "重新认证"}} {
			t.Run(mode+tc.purpose.String(), func(t *testing.T) {
				f := newSMTPFixture(t, mode, nil)
				s, err := delivery.New(f.config())
				if err != nil {
					t.Fatal(err)
				}
				if err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: tc.purpose, Code: "123456", TTL: 5 * time.Minute}); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.clearSecret || f.auth != "\x00test-user\x00synthetic-password" || f.accepted != 1 {
					t.Fatal("SMTP authentication/delivery contract failed")
				}
				msg, err := mail.ReadMessage(strings.NewReader(string(f.data)))
				if err != nil {
					t.Fatal(err)
				}
				if msg.Header.Get("From") != "sender@example.org" || msg.Header.Get("To") != "person@example.org" || msg.Header.Get("Content-Type") != "text/plain; charset=UTF-8" {
					t.Fatal("invalid email headers")
				}
				bodyBytes, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
				if err != nil {
					t.Fatal(err)
				}
				body := string(bodyBytes)
				if !strings.Contains(body, tc.word) || !strings.Contains(body, "123456") || !strings.Contains(body, "5 分钟") {
					t.Fatal("email missing purpose, code or expiration")
				}
			})
		}
	}
}

func TestSMTPAcceptedDataIgnoresQuitFailure(t *testing.T) {
	f := newSMTPFixture(t, "implicit", func(f *smtpFixture) { f.badQuit = true })
	s, err := delivery.New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}); err != nil {
		t.Fatal("DATA acceptance must not become retryable QUIT failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accepted != 1 {
		t.Fatal("message was retried or not accepted")
	}
}

func TestSMTPFailuresAreSafe(t *testing.T) {
	for _, reject := range []string{"AUTH", "MAIL", "RCPT", "DATA"} {
		t.Run(reject, func(t *testing.T) {
			f := newSMTPFixture(t, "implicit", func(f *smtpFixture) { f.reject = reject })
			s, err := delivery.New(f.config())
			if err != nil {
				t.Fatal(err)
			}
			err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute})
			if !errors.Is(err, sender.ErrUnavailable) {
				t.Fatal("SMTP refusal must be unavailable")
			}
			for _, secret := range []string{"person@example.org", "synthetic-password", "123456", "body"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("SMTP error leaked sensitive information")
				}
			}
		})
	}
}

func TestSMTPNoTLSDowngrade(t *testing.T) {
	for _, tc := range []struct {
		name, mode            string
		noStartTLS, untrusted bool
	}{{"required STARTTLS", "starttls", true, false}, {"untrusted implicit", "implicit", false, true}, {"untrusted STARTTLS", "starttls", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSMTPFixture(t, tc.mode, func(f *smtpFixture) { f.noStartTLS = tc.noStartTLS })
			cfg := f.config()
			if tc.untrusted {
				cfg.TLSConfig = nil
			}
			s, err := delivery.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}); !errors.Is(err, sender.ErrUnavailable) {
				t.Fatal("TLS failure must reject")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.auth != "" || len(f.data) != 0 {
				t.Fatal("TLS failure sent credentials or message")
			}
		})
	}
}

func TestSMTPTimeoutAndCancellation(t *testing.T) {
	for _, stage := range []string{"greeting", "AUTH", "DATA"} {
		for _, cancel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%t", stage, cancel), func(t *testing.T) {
				f := newSMTPFixture(t, "implicit", func(f *smtpFixture) { f.stall = stage })
				cfg := f.config()
				cfg.Timeout = 80 * time.Millisecond
				s, err := delivery.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				ctx, stop := context.WithCancel(context.Background())
				defer stop()
				if cancel {
					cfg.Timeout = time.Second
					s, err = delivery.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					timer := time.AfterFunc(40*time.Millisecond, stop)
					defer timer.Stop()
				}
				start := time.Now()
				err = s.SendEmail(ctx, "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute})
				if !errors.Is(err, sender.ErrUnavailable) || time.Since(start) > 500*time.Millisecond {
					t.Fatal("SMTP timeout/cancel was not bounded")
				}
			})
		}
	}
}

func TestSMTPRejectsUnsafeConfigurationAndInjection(t *testing.T) {
	valid := delivery.Config{Host: "127.0.0.1", Port: 25, From: "sender@example.org", TLSMode: "implicit", Timeout: time.Second}
	for _, change := range []func(*delivery.Config){func(c *delivery.Config) { c.From = "sender@example.org\r\nBcc: other@example.org" }, func(c *delivery.Config) { c.Host = "host\r\nMAIL" }, func(c *delivery.Config) { c.Port = 0 }, func(c *delivery.Config) { c.Timeout = -time.Second }, func(c *delivery.Config) { c.TLSMode = "optional" }, func(c *delivery.Config) { c.Username = "user" }, func(c *delivery.Config) { c.TLSConfig = &tls.Config{InsecureSkipVerify: true} }, func(c *delivery.Config) { c.From = "a@example.org,b@example.org" }} {
		cfg := valid
		change(&cfg)
		if _, err := delivery.New(cfg); err == nil {
			t.Fatal("accepted unsafe SMTP config")
		}
	}
	f := newSMTPFixture(t, "implicit", nil)
	s, err := delivery.New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		to      string
		message sender.Message
	}{{"person@example.org\r\nBcc: other@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}}, {"person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456\r\nBcc: other", TTL: time.Minute}}, {"person@example.org", sender.Message{Purpose: enum.CodePurpose(99), Code: "123456", TTL: time.Minute}}, {"person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: 0}}} {
		if err := s.SendEmail(context.Background(), tc.to, tc.message); !errors.Is(err, sender.ErrUnavailable) {
			t.Fatal("invalid message must reject safely")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connections != 0 {
		t.Fatal("invalid message reached SMTP before validation")
	}
}

func TestSMTPDevelopmentNoneWithoutAuth(t *testing.T) {
	f := newSMTPFixture(t, "none", nil)
	cfg := f.config()
	cfg.Username = ""
	cfg.Password = ""
	s, err := delivery.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}); err != nil {
		t.Fatal("explicit development plain SMTP failed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.auth != "" || f.accepted != 1 {
		t.Fatal("development SMTP unexpectedly authenticated or did not submit")
	}
}

func TestSMTPBoundsTLSHandshake(t *testing.T) {
	for _, mode := range []string{"implicit", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			f := newSMTPFixture(t, mode, func(f *smtpFixture) { f.stallTLS = true })
			cfg := f.config()
			cfg.Timeout = 80 * time.Millisecond
			s, err := delivery.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if err = s.SendEmail(context.Background(), "person@example.org", sender.Message{Purpose: enum.PurposeSignIn, Code: "123456", TTL: time.Minute}); !errors.Is(err, sender.ErrUnavailable) || time.Since(start) > 500*time.Millisecond {
				t.Fatal("TLS handshake escaped total timeout")
			}
		})
	}
}
