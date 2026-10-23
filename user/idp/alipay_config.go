package idp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// AlipayApp 配置普通公钥模式的支付宝应用；密钥仅在服务端使用。
type AlipayApp struct {
	AppID           string
	PrivateKey      string // PEM PKCS#1 或 PKCS#8 RSA 私钥，至少 2048 位。
	AlipayPublicKey string // PEM PKCS#1 或 SPKI 支付宝 RSA 公钥，至少 2048 位。
}

// AlipaySubjectMode 决定实例内所有支付宝应用的固定身份识别方式。
type AlipaySubjectMode string

const (
	AlipaySubjectModeUnionID AlipaySubjectMode = "UNIONID"
	AlipaySubjectModeOpenID  AlipaySubjectMode = "OPENID"
)

// AlipayConfig 是支付宝普通公钥模式配置；Apps 为空表示未启用。
type AlipayConfig struct {
	Apps        []AlipayApp
	SubjectMode AlipaySubjectMode // 空值默认 UNIONID。
	GatewayURL  string            // 空值默认生产网关；仅回环测试地址允许 HTTP。
}

const alipayDefaultGatewayURL = "https://openapi.alipay.com/gateway.do"

var alipayAppIDPattern = regexp.MustCompile(`^[0-9]{16}$`)

type alipayAppConfig struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

type parsedAlipayConfig struct {
	apps        map[string]alipayAppConfig
	subjectMode AlipaySubjectMode
	gatewayURL  string
}

// ValidateAlipayConfig 不做网络或文件 I/O；错误只指出配置项，不包含密钥内容。
func ValidateAlipayConfig(config AlipayConfig) error {
	_, err := parseAlipayConfig(config)
	return err
}

func parseAlipayConfig(config AlipayConfig) (parsedAlipayConfig, error) {
	result := parsedAlipayConfig{apps: make(map[string]alipayAppConfig, len(config.Apps)), subjectMode: config.SubjectMode}
	if result.subjectMode == "" {
		result.subjectMode = AlipaySubjectModeUnionID
	}
	if result.subjectMode != AlipaySubjectModeUnionID && result.subjectMode != AlipaySubjectModeOpenID {
		return parsedAlipayConfig{}, errors.New("idp: AlipaySubjectMode must be UNIONID or OPENID")
	}
	gateway, err := normalizeAlipayGateway(config.GatewayURL)
	if err != nil {
		return parsedAlipayConfig{}, err
	}
	result.gatewayURL = gateway
	for i, app := range config.Apps {
		if !alipayAppIDPattern.MatchString(app.AppID) {
			return parsedAlipayConfig{}, fmt.Errorf("idp: AlipayApps[%d].AppID must contain 16 ASCII digits", i)
		}
		if _, exists := result.apps[app.AppID]; exists {
			return parsedAlipayConfig{}, fmt.Errorf("idp: AlipayApps[%d].AppID is duplicated", i)
		}
		privateKey, err := parseAlipayPrivateKey(app.PrivateKey)
		if err != nil {
			return parsedAlipayConfig{}, fmt.Errorf("idp: AlipayApps[%d].PrivateKey: %w", i, err)
		}
		publicKey, err := parseAlipayPublicKey(app.AlipayPublicKey)
		if err != nil {
			return parsedAlipayConfig{}, fmt.Errorf("idp: AlipayApps[%d].AlipayPublicKey: %w", i, err)
		}
		result.apps[app.AppID] = alipayAppConfig{privateKey: privateKey, publicKey: publicKey}
	}
	return result, nil
}

func normalizeAlipayGateway(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return alipayDefaultGatewayURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", errors.New("idp: AlipayGatewayURL must be an absolute HTTPS URL without userinfo, query or fragment")
	}
	loopback := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("idp: AlipayGatewayURL requires HTTPS except for loopback test addresses")
	}
	return u.String(), nil
}

func decodeAlipayPEM(raw string) (*pem.Block, error) {
	raw = strings.TrimSpace(raw)
	block, rest := pem.Decode([]byte(raw))
	if block == nil || strings.TrimSpace(string(rest)) != "" || len(block.Headers) != 0 || !strings.HasPrefix(raw, "-----BEGIN "+block.Type+"-----") {
		return nil, errors.New("must contain one unencrypted PEM RSA key")
	}
	return block, nil
}

func parseAlipayPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, err := decodeAlipayPEM(raw)
	if err != nil {
		return nil, err
	}
	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var value any
		value, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		key, _ = value.(*rsa.PrivateKey)
	}
	if err != nil || key == nil || key.Validate() != nil || key.N.BitLen() < 2048 {
		return nil, errors.New("must be a valid PEM PKCS#1 or PKCS#8 RSA private key of at least 2048 bits")
	}
	return key, nil
}

func parseAlipayPublicKey(raw string) (*rsa.PublicKey, error) {
	block, err := decodeAlipayPEM(raw)
	if err != nil {
		return nil, err
	}
	var key *rsa.PublicKey
	switch block.Type {
	case "RSA PUBLIC KEY":
		key, err = x509.ParsePKCS1PublicKey(block.Bytes)
	case "PUBLIC KEY":
		var value any
		value, err = x509.ParsePKIXPublicKey(block.Bytes)
		key, _ = value.(*rsa.PublicKey)
	}
	if err != nil || key == nil || key.N.BitLen() < 2048 || key.E < 3 || key.E%2 == 0 {
		return nil, errors.New("must be a valid PEM PKCS#1 or SPKI RSA public key of at least 2048 bits")
	}
	return key, nil
}
