// Package smtp 提供可选的 SMTP 验证码发送器。提交成功只表示服务器接受 DATA，
// 不承诺最终到达邮箱；发送器不自动重试，也不输出 SMTP 响应或消息内容。
package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/mail"
	netsmtp "net/smtp"
	"reflect"
	"strconv"
	"strings"
	"time"

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
	cfg      Config
	tls      *tls.Config
	renderer Renderer
}

// New 只校验和复制配置，不进行网络 I/O。错误只包含配置项名。
func New(cfg Config) (*Sender, error) {
	return NewWithRenderer(cfg, LegacyChineseRenderer{})
}

// NewWithRenderer 只校验和复制连接配置，既不渲染也不进行网络 I/O。
// renderer 必须并发安全；发送期间的语言和模板选择由宿主管理。
func NewWithRenderer(cfg Config, renderer Renderer) (*Sender, error) {
	if renderer == nil {
		return nil, errors.New("smtp: invalid Renderer")
	}
	value := reflect.ValueOf(renderer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return nil, errors.New("smtp: invalid Renderer")
		}
	}
	// 内置渲染器的品牌在构造时校验，避免每次发送都在占用额度之后才失败。
	switch r := renderer.(type) {
	case LegacyChineseRenderer:
		if ValidateBrand(r.Brand) != nil {
			return nil, errors.New("smtp: invalid Renderer Brand")
		}
	case *LegacyChineseRenderer:
		if ValidateBrand(r.Brand) != nil {
			return nil, errors.New("smtp: invalid Renderer Brand")
		}
	}

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
	return &Sender{cfg: cfg, tls: tlsCfg, renderer: renderer}, nil
}

func validAddress(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value && addr.Name == ""
}

// SendEmail 使用有限总预算完成渲染、连接、TLS、AUTH 与 DATA；取消会关闭整个连接。
// 对外始终返回固定安全错误，避免 SMTP 服务端响应泄漏收件地址或凭据。
func (s *Sender) SendEmail(ctx context.Context, to string, m sender.CodeMessage) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	if ctx.Err() != nil || !validAddress(to) || !validCodeMessage(m) {
		return sender.ErrUnavailable
	}
	content, err := s.renderer.Render(ctx, m)
	if err != nil || ctx.Err() != nil {
		return sender.ErrUnavailable
	}
	data, err := message(s.cfg.From, to, content)
	if err != nil || ctx.Err() != nil {
		return sender.ErrUnavailable
	}
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

var _ sender.EmailCodeSender = (*Sender)(nil)
