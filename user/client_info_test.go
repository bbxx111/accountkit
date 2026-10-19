package user

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestNormalizeClientInfo(t *testing.T) {
	if got, err := NormalizeClientInfo(nil); got != nil || err != nil {
		t.Fatalf("nil: %v, %v", got, err)
	}
	if got, err := NormalizeClientInfo(&ClientInfo{}); got != nil || err != nil {
		t.Fatalf("empty: %v, %v", got, err)
	}
	input := ClientInfo{ClientType: enum.ClientWeb, OSName: " Android ", BrowserName: "Chrome", BrowserVersion: " 123 ", AppName: "ignored", AppVersion: "1"}
	before := input
	got, err := NormalizeClientInfo(&input)
	want := &ClientInfo{ClientType: enum.ClientWeb, OSName: "Android", BrowserName: "Chrome", BrowserVersion: "123"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if input != before || got == &input {
		t.Fatal("normalization changed or aliased input")
	}
	for name, in := range map[string]ClientInfo{
		"client enum": {ClientType: 99}, "device enum": {DeviceType: -1},
		"control": {OSName: "Android\n"}, "C1 control": {AppName: "a\u0085"},
		"invalid utf8": {OSName: string([]byte{255})},
		"name limit":   {OSName: strings.Repeat("a", 65)}, "model limit": {DeviceModel: strings.Repeat("a", 65)},
		"version limit": {OSVersion: strings.Repeat("a", 33)}, "description limit": {OSName: strings.Repeat("a", 2049)},
	} {
		t.Run(name, func(t *testing.T) {
			before := in
			out, err := NormalizeClientInfo(&in)
			if !errors.Is(err, ErrInvalidClientInfo) || out != nil {
				t.Fatalf("got %+v, %v", out, err)
			}
			if in != before {
				t.Fatal("input mutated")
			}
		})
	}
	unicodeName := strings.Repeat("界", 64)
	if got, err := NormalizeClientInfo(&ClientInfo{OSName: unicodeName}); err != nil || got.OSName != unicodeName {
		t.Fatalf("64 rune name: %+v %v", got, err)
	}
	if got, err := NormalizeClientInfo(&ClientInfo{BrowserVersion: "123"}); err != nil || got == nil || got.BrowserVersion != "123" {
		t.Fatalf("version observation: %+v %v", got, err)
	}
	native, err := NormalizeClientInfo(&ClientInfo{ClientType: enum.ClientNative, BrowserName: "Chrome", BrowserVersion: "1", AppName: "Sample"})
	if err != nil || native.BrowserName != "" || native.BrowserVersion != "" || native.AppName != "Sample" {
		t.Fatalf("native: %+v %v", native, err)
	}
}

func TestMergeClientInfo(t *testing.T) {
	if got := MergeClientInfo(nil, nil); got != nil {
		t.Fatalf("nil: %+v", got)
	}
	previous := ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", OSVersion: "10", BrowserName: "Chrome", BrowserVersion: "123", DeviceType: enum.DeviceDesktop}
	for _, tc := range []struct {
		name     string
		observed *ClientInfo
		want     ClientInfo
	}{
		{"missing retains", nil, previous},
		{"same name new version", &ClientInfo{BrowserVersion: "124"}, ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", OSVersion: "10", BrowserName: "Chrome", BrowserVersion: "124", DeviceType: enum.DeviceDesktop}},
		{"browser switch clears version", &ClientInfo{BrowserName: "Safari"}, ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", OSVersion: "10", BrowserName: "Safari", DeviceType: enum.DeviceDesktop}},
		{"os switch clears version", &ClientInfo{OSName: "Linux"}, ClientInfo{ClientType: enum.ClientWeb, OSName: "Linux", BrowserName: "Chrome", BrowserVersion: "123", DeviceType: enum.DeviceDesktop}},
		{"native clears browser", &ClientInfo{ClientType: enum.ClientNative, AppName: "Sample", AppVersion: "2"}, ClientInfo{ClientType: enum.ClientNative, OSName: "Windows", OSVersion: "10", AppName: "Sample", AppVersion: "2", DeviceType: enum.DeviceDesktop}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := previous
			var observedBefore ClientInfo
			if tc.observed != nil {
				observedBefore = *tc.observed
			}
			got := MergeClientInfo(&previous, tc.observed)
			if !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if previous != before || got == &previous || (tc.observed != nil && *tc.observed != observedBefore) {
				t.Fatal("input mutation/alias")
			}
		})
	}
	native := &ClientInfo{ClientType: enum.ClientNative, AppName: "A", AppVersion: "1"}
	if got := MergeClientInfo(native, &ClientInfo{AppName: "B"}); got.AppVersion != "" {
		t.Fatalf("app version mismatch: %+v", got)
	}
	if got := MergeClientInfo(native, &ClientInfo{ClientType: enum.ClientWeb, BrowserName: "Chrome"}); got.AppName != "" || got.AppVersion != "" {
		t.Fatalf("web app fields: %+v", got)
	}
	if got := MergeClientInfo(nil, &ClientInfo{OSVersion: "1"}); got != nil {
		t.Fatalf("orphan version: %+v", got)
	}
}
