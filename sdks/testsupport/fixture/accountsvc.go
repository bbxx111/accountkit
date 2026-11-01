package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"

	"errors"
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc"
	"github.com/bbxx111/accountkit/user/sender"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"

	"net/mail"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"time"
)

// startSMTP captures plain loopback-only development mail. Production SMTP remains untouched.
func startSMTP(s *capture) (net.Listener, error) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
				reader := textproto.NewReader(rw.Reader)
				send := func(s string) { fmt.Fprint(rw, s+"\r\n"); rw.Flush() }
				send("220 synthetic SMTP")
				target := ""
				for {
					line, e := reader.ReadLine()
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						send("250 fixture")
					case strings.HasPrefix(line, "MAIL FROM:"):
						send("250 ok")
					case strings.HasPrefix(line, "RCPT TO:"):
						target = strings.TrimSuffix(strings.TrimPrefix(line, "RCPT TO:<"), ">")
						send("250 ok")
					case line == "DATA":
						send("354 data")
						body, e := io.ReadAll(reader.DotReader())
						if e != nil {
							return
						}
						decoded := decodeMail(body)
						code := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(decoded)
						if code == "" {
							send("550 missing code")
							continue
						}
						purpose := enum.PurposeSignIn
						lower := strings.ToLower(decoded)
						if strings.Contains(lower, "完成登录并关联身份") || strings.Contains(lower, "finish signing in") {
							purpose = enum.PurposeSignInComplete
						} else if strings.Contains(lower, "确认本人身份") || strings.Contains(lower, "confirming your identity") {
							purpose = enum.PurposeReauth
						} else if strings.Contains(lower, "邮箱绑定") || strings.Contains(lower, "add this email") {
							purpose = enum.PurposeBind
						}
						s.save(target, sender.CodeMessage{Code: code, Purpose: purpose})
						send("250 queued")
					case line == "QUIT":
						send("221 bye")
						return
					default:
						send("250 ok")
					}
				}
			}()
		}
	}()
	return ln, nil
}
func serviceEnv(key *ecdsa.PrivateKey, o options, controlOrigin, serviceAddr string, smtp net.Listener) error {
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return e
	}
	encoded := base64.StdEncoding.EncodeToString(der)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	_, port, _ := net.SplitHostPort(smtp.Addr().String())
	values := map[string]string{"MODE": "development", "HTTP_ADDR": serviceAddr, "TLS_ENABLED": "false", "DATABASE_URL": o.database, "REDIS_URL": "redis://" + o.redis + "/0", "AUTH_SCHEMA": "sdk_fixture_service", "AUTH_KEY_PREFIX": "sdk_fixture_service:", "JWT_KEYS": "1:" + encoded, "JWT_ACTIVE_KEY": "1", "JWT_ISSUER": controlOrigin, "JWT_AUDIENCE": "sdk-consumer", "SUBJECT_CIPHER_KEYS": "1:" + base64.StdEncoding.EncodeToString(secret), "SUBJECT_CIPHER_ACTIVE_KEY": "1", "SUBJECT_HMAC_KEYS": "1:" + base64.StdEncoding.EncodeToString(secret), "SUBJECT_HMAC_ACTIVE_KEY": "1", "ENABLED_IDENTITY_KINDS": "EMAIL", "REQUIRED_IDENTITY_KINDS": "[]", "SMTP_HOST": "127.0.0.1", "SMTP_PORT": port, "SMTP_FROM": "fixture@example.test", "SMTP_TLS_MODE": "none", "ADMIN_ENABLED": "true", "ADMIN_ISSUER": controlOrigin, "ADMIN_AUDIENCE": "sdk-admin", "CODE_COOLDOWN": "1s", "MAINTENANCE_INTERVAL": "1h"}
	for name, value := range values {
		if e = os.Setenv("ACCOUNTSVC_"+name, value); e != nil {
			return e
		}
	}
	return nil
}
func runService(ctx context.Context, key *ecdsa.PrivateKey, o options, controlOrigin string, s *capture) (string, context.CancelFunc, error) {
	smtp, e := startSMTP(s)
	if e != nil {
		return "", nil, e
	}
	serviceAddr := os.Getenv("SDK_FIXTURE_SERVICE_ADDR")
	if serviceAddr == "" {
		serviceAddr = "127.0.0.1:18082"
	}
	host, _, e := net.SplitHostPort(serviceAddr)
	if e != nil || host != "127.0.0.1" {
		smtp.Close()
		return "", nil, errors.New("service fixture must bind loopback")
	}
	if e = serviceEnv(key, o, controlOrigin, serviceAddr, smtp); e != nil {
		smtp.Close()
		return "", nil, e
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { defer smtp.Close(); done <- accountsvc.Run(child, []string{"serve"}, slog.Default()) }()
	origin := "http://" + serviceAddr
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case e := <-done:
			cancel()
			if e == nil {
				e = errors.New("accountsvc stopped before ready")
			}
			return "", nil, e
		case <-ctx.Done():
			cancel()
			return "", nil, ctx.Err()
		case <-deadline.C:
			cancel()
			return "", nil, errors.New("accountsvc readiness timeout")
		case <-ticker.C:
			if probeReady(ctx, origin) {
				select {
				case e := <-done:
					cancel()
					if e == nil {
						e = errors.New("accountsvc stopped before ready")
					}
					return "", nil, e
				default:
				}
				return origin, func() { cancel(); <-done }, nil
			}
		}
	}
}

func decodeMail(raw []byte) string {
	m, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil {
		return ""
	}
	contentType, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/") {
		reader := multipart.NewReader(m.Body, params["boundary"])
		for {
			part, e := reader.NextPart()
			if e != nil {
				return ""
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
				b, _ := io.ReadAll(part)
				return string(b)
			}
		}
	}
	var reader io.Reader = m.Body
	if m.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
		reader = quotedprintable.NewReader(reader)
	}
	b, _ := io.ReadAll(reader)
	return string(b)
}
