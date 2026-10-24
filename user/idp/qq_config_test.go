package idp

import (
	"strings"
	"testing"
)

func TestQQConfigValidation(t *testing.T) {
	ok := []QQConfig{
		{},
		{AppIDs: []string{"1100000001", "1100000002"}},
		{AppIDs: []string{"1"}, SubjectMode: QQSubjectModeOpenID},
		{AppIDs: []string{"1100000001"}, APIBaseURL: "http://127.0.0.1:8080/"},
		{AppIDs: []string{"1100000001"}, APIBaseURL: "https://proxy.example.test/qq"},
	}
	for _, c := range ok {
		if err := ValidateQQConfig(c); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	bad := map[string]QQConfig{
		"QQAppIDs[0]":     {AppIDs: []string{""}},
		"QQAppIDs[1]":     {AppIDs: []string{"1100000001", "1100000001"}},
		"QQAppIDs[0] ":    {AppIDs: []string{"app-one"}},
		"QQAppIDs[0]  ":   {AppIDs: []string{strings.Repeat("1", 21)}},
		"QQSubjectMode":   {SubjectMode: "unionid"},
		"QQSubjectMode ":  {SubjectMode: "AUTO"},
		"QQAPIBaseURL":    {APIBaseURL: "http://graph.qq.com"},
		"QQAPIBaseURL ":   {APIBaseURL: "https://u:p@graph.qq.com"},
		"QQAPIBaseURL  ":  {APIBaseURL: "https://graph.qq.com?x=1"},
		"QQAPIBaseURL   ": {APIBaseURL: "https://graph.qq.com#f"},
	}
	for want, c := range bad {
		err := ValidateQQConfig(c)
		if err == nil || !strings.HasPrefix(err.Error(), "idp: "+strings.TrimSpace(want)) {
			t.Fatalf("%+v: %v", c, err)
		}
		for _, secret := range c.AppIDs {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Fatal("error echoed configuration value")
			}
		}
	}
	parsed, err := parseQQConfig(QQConfig{AppIDs: []string{"1100000001"}, APIBaseURL: "https://proxy.example.test/qq/"})
	if err != nil || parsed.baseURL != "https://proxy.example.test/qq" || parsed.subjectMode != QQSubjectModeUnionID {
		t.Fatalf("normalized config: %+v %v", parsed, err)
	}
	if parsed, _ := parseQQConfig(QQConfig{}); parsed.baseURL != "https://graph.qq.com" {
		t.Fatal("default base URL")
	}
	apps := []string{"1100000001"}
	parsed, _ = parseQQConfig(QQConfig{AppIDs: apps})
	apps[0] = "changed"
	if !parsed.apps["1100000001"] || parsed.appIDs[0] != "1100000001" {
		t.Fatal("parsed config aliases caller slice")
	}
}
