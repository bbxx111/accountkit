package clientinfo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

const windowsChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
const iosSafari = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1"
const androidChrome = "Mozilla/5.0 (Linux; Android 13; Pixel 7 Build/TQ3A) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.6312.80 Mobile Safari/537.36"

func TestParseClientInfo(t *testing.T) {
	windows := &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", OSVersion: "10.0", BrowserName: "Chrome", BrowserVersion: "123.0.0.0", DeviceType: enum.DeviceDesktop}
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    *user.ClientInfo
		invalid bool
	}{
		{name: "empty"},
		{name: "Windows Chrome", headers: http.Header{"User-Agent": {windowsChrome}}, want: windows},
		{name: "iOS Safari", headers: http.Header{"User-Agent": {iosSafari}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "iOS", OSVersion: "17.4", BrowserName: "Safari", BrowserVersion: "17.4", DeviceType: enum.DevicePhone, DeviceModel: "iPhone"}},
		{name: "Android Chrome", headers: http.Header{"User-Agent": {androidChrome}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Android", OSVersion: "13", BrowserName: "Chrome", BrowserVersion: "123.0.6312.80", DeviceType: enum.DevicePhone, DeviceModel: "Pixel 7"}},
		{name: "native explicit", headers: http.Header{"User-Agent": {windowsChrome}, "X-Client-Type": {"NATIVE"}, "X-Client-Os-Name": {" Android "}, "X-Client-Os-Version": {"14"}, "X-Client-App-Name": {"Sample"}, "X-Client-App-Version": {"2"}, "X-Device-Type": {"PHONE"}, "X-Device-Model": {"Synthetic"}}, want: &user.ClientInfo{ClientType: enum.ClientNative, OSName: "Android", OSVersion: "14", BrowserName: "Chrome", BrowserVersion: "123.0.0.0", AppName: "Sample", AppVersion: "2", DeviceType: enum.DevicePhone, DeviceModel: "Synthetic"}},
		{name: "GREASE and multiple brands", headers: http.Header{"Sec-Ch-Ua": {`"Not A;Brand";v="99", "Chromium";v="123", "Google Chrome";v="123"`}, "Sec-Ch-Ua-Platform": {`"Windows"`}, "Sec-Ch-Ua-Mobile": {"?0"}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", BrowserName: "Chrome", BrowserVersion: "123", DeviceType: enum.DeviceDesktop}},
		{name: "Edge beats Chromium irrespective order", headers: http.Header{"Sec-Ch-Ua": {`"Microsoft Edge";v="123", "Google Chrome";v="123", "Chromium";v="123"`}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, BrowserName: "Edge", BrowserVersion: "123"}},
		{name: "full versions when available", headers: http.Header{"Sec-Ch-Ua": {`"Google Chrome";v="123"`}, "Sec-Ch-Ua-Full-Version-List": {`"Google Chrome";v="123.0.6312.80"`}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, BrowserName: "Chrome", BrowserVersion: "123.0.6312.80"}},
		{name: "CH takes priority", headers: http.Header{"User-Agent": {windowsChrome}, "Sec-Ch-Ua": {`"Microsoft Edge";v="124"`}, "Sec-Ch-Ua-Platform": {`"Android"`}, "Sec-Ch-Ua-Mobile": {"?1"}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Android", BrowserName: "Edge", BrowserVersion: "124", DeviceType: enum.DevicePhone}},
		{name: "explicit name clears unrelated version", headers: http.Header{"User-Agent": {windowsChrome}, "X-Client-Browser-Name": {"Safari"}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Windows", OSVersion: "10.0", BrowserName: "Safari", DeviceType: enum.DeviceDesktop}},
		{name: "explicit version observation", headers: http.Header{"X-Client-App-Version": {"2"}}, want: &user.ClientInfo{AppVersion: "2"}},
		{name: "unknown GREASE only", headers: http.Header{"Sec-Ch-Ua": {`"Not.A/Brand";v="99"`}}},
		{name: "duplicate UA ignored", headers: http.Header{"User-Agent": {windowsChrome, iosSafari}}},
		{name: "unbalanced UA ignored", headers: http.Header{"User-Agent": {windowsChrome + " ("}}},
		{name: "UA over budget", headers: http.Header{"User-Agent": {windowsChrome + strings.Repeat("a", 2049)}}},
		{name: "malformed hints fallback", headers: http.Header{"User-Agent": {windowsChrome}, "Sec-Ch-Ua": {`"Google Chrome";v="123`}}, want: windows},
		{name: "duplicate hints fallback", headers: http.Header{"User-Agent": {windowsChrome}, "Sec-Ch-Ua": {`"Microsoft Edge";v="123"`, `"Google Chrome";v="123"`}}, want: windows},
		{name: "wrong hint item type", headers: http.Header{"User-Agent": {windowsChrome}, "Sec-Ch-Ua-Platform": {"123"}}, want: windows},
		{name: "hint aggregate over budget", headers: http.Header{"User-Agent": {windowsChrome}, "Sec-Ch-Ua": {`"Microsoft Edge";v="123"`}, "Sec-Ch-Ua-Model": {`"` + strings.Repeat("a", 2048) + `"`}}, want: windows},
		{name: "malformed individual hint preserves others", headers: http.Header{"Sec-Ch-Ua": {`"Google Chrome";v="123"`}, "Sec-Ch-Ua-Platform": {`"Android`}}, want: &user.ClientInfo{ClientType: enum.ClientWeb, BrowserName: "Chrome", BrowserVersion: "123"}},
		{name: "BFF Go", headers: http.Header{"User-Agent": {"Go-http-client/1.1"}}},
		{name: "BFF curl", headers: http.Header{"User-Agent": {"curl/8.10.1"}}},
		{name: "no forwarded trust", headers: http.Header{"User-Agent": {"Go-http-client/1.1"}, "X-Forwarded-User-Agent": {windowsChrome}}},
		{name: "invalid client type", headers: http.Header{"X-Client-Type": {"web"}}, invalid: true},
		{name: "invalid device type", headers: http.Header{"X-Device-Type": {"UNSPECIFIED"}}, invalid: true},
		{name: "invalid name", headers: http.Header{"X-Client-Os-Name": {"test\n"}}, invalid: true},
		{name: "invalid long version", headers: http.Header{"X-Client-App-Version": {strings.Repeat("1", 33)}}, invalid: true},
		{name: "duplicate explicit", headers: http.Header{"X-Client-App-Name": {"A", "B"}}, invalid: true},
		{name: "invalid inapplicable still rejected", headers: http.Header{"X-Client-Type": {"NATIVE"}, "X-Client-Browser-Name": {strings.Repeat("a", 65)}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			r.Header = tc.headers.Clone()
			before := r.Header.Clone()
			got, err := Parse(r)
			if tc.invalid {
				if !errors.Is(err, user.ErrInvalidClientInfo) || got != nil {
					t.Fatalf("got %+v %v", got, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v %v; want %+v", got, err, tc.want)
			}
			if !reflect.DeepEqual(r.Header, before) {
				t.Fatal("request headers mutated")
			}
		})
	}
	if got, err := Parse(nil); got != nil || err != nil {
		t.Fatalf("nil request: %+v %v", got, err)
	}
}
