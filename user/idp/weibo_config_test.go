package idp

import (
	"strings"
	"testing"
)

func TestWeiboConfigValidation(t *testing.T) {
	ok := []WeiboConfig{
		{},
		{AppIDs: []string{"1234567890", "1234567891"}},
		{AppIDs: []string{"1"}},
		{AppIDs: []string{"1234567890"}, APIBaseURL: "http://127.0.0.1:8080/"},
		{AppIDs: []string{"1234567890"}, APIBaseURL: "https://proxy.example.test/weibo"},
	}
	for _, c := range ok {
		if err := ValidateWeiboConfig(c); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	bad := map[string]WeiboConfig{
		"WeiboAppIDs[0]":      {AppIDs: []string{""}},
		"WeiboAppIDs[1]":      {AppIDs: []string{"1234567890", "1234567890"}},
		"WeiboAppIDs[0] ":     {AppIDs: []string{"app-key"}},
		"WeiboAppIDs[0]  ":    {AppIDs: []string{strings.Repeat("1", 21)}},
		"WeiboAPIBaseURL":     {APIBaseURL: "http://api.weibo.com"},
		"WeiboAPIBaseURL ":    {APIBaseURL: "https://u:p@api.weibo.com"},
		"WeiboAPIBaseURL  ":   {APIBaseURL: "https://api.weibo.com?x=1"},
		"WeiboAPIBaseURL   ":  {APIBaseURL: "https://api.weibo.com#f"},
		"WeiboAPIBaseURL    ": {APIBaseURL: "ftp://api.weibo.com"},
	}
	for want, c := range bad {
		err := ValidateWeiboConfig(c)
		if err == nil || !strings.HasPrefix(err.Error(), "idp: "+strings.TrimSpace(want)) {
			t.Fatalf("%+v: %v", c, err)
		}
		for _, value := range c.AppIDs {
			if value != "" && strings.Contains(err.Error(), value) {
				t.Fatal("error echoed configuration value")
			}
		}
		if c.APIBaseURL != "" && strings.Contains(err.Error(), c.APIBaseURL) {
			t.Fatal("error echoed configuration value")
		}
	}
	parsed, err := parseWeiboConfig(WeiboConfig{AppIDs: []string{"1234567890"}, APIBaseURL: "https://proxy.example.test/weibo/"})
	if err != nil || parsed.baseURL != "https://proxy.example.test/weibo" {
		t.Fatalf("normalized config: %+v %v", parsed, err)
	}
	if parsed, _ := parseWeiboConfig(WeiboConfig{}); parsed.baseURL != "https://api.weibo.com" {
		t.Fatal("default base URL")
	}
	apps := []string{"1234567890"}
	parsed, _ = parseWeiboConfig(WeiboConfig{AppIDs: apps})
	apps[0] = "changed"
	if !parsed.apps["1234567890"] || parsed.appIDs[0] != "1234567890" {
		t.Fatal("parsed config aliases caller slice")
	}
}
