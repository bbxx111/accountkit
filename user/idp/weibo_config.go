package idp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// WeiboConfig 配置微博开放平台移动应用；AppIDs 即微博 AppKey，为空表示未启用。App SDK 校验不需要 AppSecret。
type WeiboConfig struct {
	AppIDs     []string
	APIBaseURL string // 空值默认 https://api.weibo.com；仅回环测试地址允许 HTTP。
}

const weiboDefaultAPIBaseURL = "https://api.weibo.com"

var weiboAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

type parsedWeiboConfig struct {
	apps    map[string]bool
	appIDs  []string
	baseURL string
}

// ValidateWeiboConfig 不做网络或文件 I/O；错误只指出配置项，不包含配置值。
func ValidateWeiboConfig(config WeiboConfig) error {
	_, err := parseWeiboConfig(config)
	return err
}

func parseWeiboConfig(config WeiboConfig) (parsedWeiboConfig, error) {
	result := parsedWeiboConfig{apps: make(map[string]bool, len(config.AppIDs))}
	base, err := normalizeWeiboBaseURL(config.APIBaseURL)
	if err != nil {
		return parsedWeiboConfig{}, err
	}
	result.baseURL = base
	for i, appID := range config.AppIDs {
		if !weiboAppIDPattern.MatchString(appID) {
			return parsedWeiboConfig{}, fmt.Errorf("idp: WeiboAppIDs[%d] must contain 1-20 ASCII digits", i)
		}
		if result.apps[appID] {
			return parsedWeiboConfig{}, fmt.Errorf("idp: WeiboAppIDs[%d] is duplicated", i)
		}
		result.apps[appID] = true
		result.appIDs = append(result.appIDs, appID)
	}
	return result, nil
}

// normalizeWeiboBaseURL 与 QQ 接口地址规则一致，并去掉尾随斜杠以便拼接接口路径。
func normalizeWeiboBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return weiboDefaultAPIBaseURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", errors.New("idp: WeiboAPIBaseURL must be an absolute HTTPS URL without userinfo, query or fragment")
	}
	loopback := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("idp: WeiboAPIBaseURL requires HTTPS except for loopback test addresses")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
