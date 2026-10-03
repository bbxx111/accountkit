// Package smtp 提供可选的 SMTP 验证码发送器。提交成功只表示服务器接受 DATA，
// 不承诺最终到达邮箱；发送器不自动重试，也不输出 SMTP 响应或消息内容。
package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

// Config 指定 SMTP 连接与认证。TLSMode 为 implicit（默认）、starttls 或
// 显式 none。none 仅供开发宿主使用；生产策略由宿主校验。
type Config struct {
	Host      string
	Port      int
	From      string
	Username  string
	Password  string
	TLSMode   string
	Timeout   time.Duration
	TLSConfig *tls.Config
}

// Sender 复用不可变配置，每次发送建立一个独立连接，不持有后台任务。
type Sender struct {
	cfg Config
	tls *tls.Config
}

// New 只校验和复制配置，不进行网络 I/O。错误只包含配置项名。
func New(cfg Config) (*Sender, error) {
	if cfg.Host == "" || strings.ContainsAny(cfg.Host, "\r\n\t /\\") {
		return nil, errors.New("smtp: invalid Host")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("smtp: invalid Port")
	}
	if !validAddress(cfg.From) {
		return nil, errors.New("smtp: invalid From")
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		return nil, errors.New("smtp: Username and Password must be paired")
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "implicit"
	}
	if cfg.TLSMode != "implicit" && cfg.TLSMode != "starttls" && cfg.TLSMode != "none" {
		return nil, errors.New("smtp: invalid TLSMode")
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("smtp: invalid Timeout")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	tlsCfg := &tls.Config{}
	if cfg.TLSConfig != nil {
		tlsCfg = cfg.TLSConfig.Clone()
	}
	if tlsCfg.InsecureSkipVerify {
		return nil, errors.New("smtp: insecure TLSConfig")
	}
	if tlsCfg.MaxVersion != 0 && tlsCfg.MaxVersion < tls.VersionTLS12 {
		return nil, errors.New("smtp: invalid TLSConfig version")
	}
	if tlsCfg.MinVersion < tls.VersionTLS12 {
		tlsCfg.MinVersion = tls.VersionTLS12
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = cfg.Host
	}
	if tlsCfg.RootCAs != nil {
		tlsCfg.RootCAs = tlsCfg.RootCAs.Clone()
	}
	// 不保留调用方 TLSConfig，防止调用方修改配置影响后续发送。
	cfg.TLSConfig = nil
	return &Sender{cfg: cfg, tls: tlsCfg}, nil
}

func validAddress(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value && addr.Name == ""
}

func message(from, to string, m sender.Message) ([]byte, error) {
	var purpose string
	switch m.Purpose {
	case enum.PurposeSignIn:
		purpose = "登录"
	case enum.PurposeBind:
		purpose = "绑定"
	case enum.PurposeReauth:
		purpose = "重新认证"
	default:
		return nil, sender.ErrUnavailable
	}
	if !validAddress(to) || m.TTL <= 0 || m.Code == "" {
		return nil, sender.ErrUnavailable
	}
	for _, digit := range m.Code {
		if digit < '0' || digit > '9' {
			return nil, sender.ErrUnavailable
		}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", from, to, mime.QEncoding.Encode("UTF-8", purpose+"验证码"))
	body := quotedprintable.NewWriter(&buf)
	// 向上取整以免正的短有效期显示为零分钟；实际校验仍由验证码存储控制。
	fmt.Fprintf(body, "您正在进行%s。\r\n\r\n验证码：%s\r\n有效期：%.0f 分钟。\r\n\r\n请勿向他人透露验证码。如非本人操作，请忽略此邮件。\r\n", purpose, m.Code, math.Ceil(m.TTL.Minutes()))
	if err := body.Close(); err != nil {
		return nil, sender.ErrUnavailable
	}
	return buf.Bytes(), nil
}

// SendEmail 使用有限总预算完成连接、TLS、AUTH 与 DATA；取消会关闭整个连接。
// 对外始终返回固定安全错误，避免 SMTP 服务端响应泄漏收件地址或凭据。
func (s *Sender) SendEmail(ctx context.Context, to string, m sender.Message) error {
	data, err := message(s.cfg.From, to, m)
	if err != nil {
		return sender.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return sender.ErrUnavailable
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return sender.ErrUnavailable
	}
	// 固定原始连接供取消回调关闭，避免 TLS 包装时重赋 conn 与回调竞争。
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	if s.cfg.TLSMode == "implicit" {
		secured := tls.Client(conn, s.tls)
		if err = secured.HandshakeContext(ctx); err != nil {
			return sender.ErrUnavailable
		}
		conn = secured
	}
	client, err := netsmtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return sender.ErrUnavailable
	}
	defer client.Close()
	if s.cfg.TLSMode == "starttls" {
		supported, _ := client.Extension("STARTTLS")
		if !supported {
			return sender.ErrUnavailable
		}
		if err = client.StartTLS(s.tls); err != nil {
			return sender.ErrUnavailable
		}
	}
	if s.cfg.Username != "" {
		if err = client.Auth(netsmtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return sender.ErrUnavailable
		}
	}
	if err = client.Mail(s.cfg.From); err != nil {
		return sender.ErrUnavailable
	}
	if err = client.Rcpt(to); err != nil {
		return sender.ErrUnavailable
	}
	writer, err := client.Data()
	if err != nil {
		return sender.ErrUnavailable
	}
	if _, err = io.Copy(writer, bytes.NewReader(data)); err != nil {
		return sender.ErrUnavailable
	}
	if err = writer.Close(); err != nil {
		return sender.ErrUnavailable
	}
	// DATA 的最终 250 是提交成功边界；QUIT 失败不能诱使调用方重发已接受的消息。
	_ = client.Quit()
	return nil
}

var _ sender.EmailSender = (*Sender)(nil)
