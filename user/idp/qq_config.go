package idp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// QQSubjectMode 决定实例内所有 QQ 应用的固定身份识别方式。
type QQSubjectMode string

// QQ 身份识别模式：UNIONID 使用 QQ 互联 unionid，OPENID 使用应用内 openid。
const (
	QQSubjectModeUnionID QQSubjectMode = "UNIONID"
	QQSubjectModeOpenID  QQSubjectMode = "OPENID"
)

// QQConfig 配置 QQ 互联移动应用；AppIDs 为空表示未启用。App SDK 校验不需要 AppKey。
type QQConfig struct {
	AppIDs      []string
	SubjectMode QQSubjectMode // 空值默认 UNIONID。
	APIBaseURL  string        // 空值默认 https://graph.qq.com；仅回环测试地址允许 HTTP。
}

const qqDefaultAPIBaseURL = "https://graph.qq.com"

var qqAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

type parsedQQConfig struct {
	apps        map[string]bool
	appIDs      []string
	subjectMode QQSubjectMode
	baseURL     string
}

// ValidateQQConfig 不做网络或文件 I/O；错误只指出配置项，不包含配置值。
func ValidateQQConfig(config QQConfig) error {
	_, err := parseQQConfig(config)
	return err
}

func parseQQConfig(config QQConfig) (parsedQQConfig, error) {
	result := parsedQQConfig{apps: make(map[string]bool, len(config.AppIDs)), subjectMode: config.SubjectMode}
	if result.subjectMode == "" {
		result.subjectMode = QQSubjectModeUnionID
	}
	if result.subjectMode != QQSubjectModeUnionID && result.subjectMode != QQSubjectModeOpenID {
		return parsedQQConfig{}, errors.New("idp: QQSubjectMode must be UNIONID or OPENID")
	}
	base, err := normalizeQQBaseURL(config.APIBaseURL)
	if err != nil {
		return parsedQQConfig{}, err
	}
	result.baseURL = base
	for i, appID := range config.AppIDs {
		if !qqAppIDPattern.MatchString(appID) {
			return parsedQQConfig{}, fmt.Errorf("idp: QQAppIDs[%d] must contain 1-20 ASCII digits", i)
		}
		if result.apps[appID] {
			return parsedQQConfig{}, fmt.Errorf("idp: QQAppIDs[%d] is duplicated", i)
		}
		result.apps[appID] = true
		result.appIDs = append(result.appIDs, appID)
	}
	return result, nil
}

// normalizeQQBaseURL 与支付宝网关规则一致，并去掉尾随斜杠以便拼接接口路径。
func normalizeQQBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return qqDefaultAPIBaseURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", errors.New("idp: QQAPIBaseURL must be an absolute HTTPS URL without userinfo, query or fragment")
	}
	loopback := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("idp: QQAPIBaseURL requires HTTPS except for loopback test addresses")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
