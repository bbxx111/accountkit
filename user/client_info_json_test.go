package user

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestClientInfoJSONRoundTrip(t *testing.T) {
	in := &ClientInfo{ClientType: enum.ClientNative, OSName: " Android ", OSVersion: "14", DeviceType: enum.DevicePhone, DeviceManufacturer: "Example", DeviceModel: "Model", AppName: "App", AppVersion: "2", AppBuild: "200", BrowserName: "Chrome", BrowserVersion: "123", Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`{"revision":9007199254740993,"flags":[true,null,"ok"]}`)}}
	want := `{"type":"NATIVE","os":{"name":"Android","version":"14"},"device":{"type":"PHONE","manufacturer":"Example","model":"Model"},"app":{"name":"App","version":"2","build":"200"},"browser":{"name":"Chrome","version":"123"},"extensions":{"engine":{"flags":[true,null,"ok"],"revision":9007199254740993}}}`
	got, err := MarshalClientInfo(in)
	if err != nil || string(got) != want {
		t.Fatalf("encoded=%s err=%v", got, err)
	}
	decoded, err := UnmarshalClientInfo(got)
	if err != nil || decoded.OSName != "Android" || decoded.AppBuild != "200" || decoded.BrowserName != "Chrome" || string(decoded.Extensions["engine"]) != `{"flags":[true,null,"ok"],"revision":9007199254740993}` {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if in.OSName != " Android " {
		t.Fatal("input changed")
	}
	for i := range got {
		got[i] = ' '
	}
	if decoded.OSName != "Android" || string(decoded.Extensions["engine"]) != `{"flags":[true,null,"ok"],"revision":9007199254740993}` {
		t.Fatal("decoded value aliases input")
	}
	for _, raw := range []string{`{}`, `{"os":{},"device":{},"app":{},"browser":{},"extensions":{}}`} {
		if out, err := UnmarshalClientInfo([]byte(raw)); err != nil || out != nil {
			t.Fatalf("empty: %+v %v", out, err)
		}
	}
	if raw, err := MarshalClientInfo(nil); err != nil || string(raw) != "null" {
		t.Fatalf("nil: %s %v", raw, err)
	}
}

func TestClientInfoJSONRejectsInvalidInput(t *testing.T) {
	for name, raw := range map[string]string{
		"null root": `null`, "array root": `[]`, "trailing": `{} {}`,
		"unknown root": `{"os_name":"Android"}`, "unknown nested": `{"os":{"arch":"arm"}}`,
		"duplicate root": `{"type":"WEB","type":"NATIVE"}`, "duplicate nested": `{"os":{"name":"a","name":"b"}}`,
		"duplicate extension": `{"extensions":{"engine":{"a":1,"a":2}}}`,
		"escaped duplicate":   `{"extensions":{"engine":{"a":1,"\u0061":2}}}`,
		"null member":         `{"os":null}`, "null text": `{"os":{"name":null}}`, "number text": `{"app":{"version":2}}`,
		"enum": `{"type":"UNSPECIFIED"}`, "device enum": `{"device":{"type":"phone"}}`,
		"NaN": `{"extensions":{"engine":NaN}}`, "Infinity": `{"extensions":{"engine":Infinity}}`,
		"control": `{"os":{"name":"a\n"}}`, "extension control": `{"extensions":{"engine":"a\u0085"}}`,
		"invalid utf8":   "{\"os\":{\"name\":\"" + string([]byte{255}) + "\"}}",
		"extension utf8": "{\"extensions\":{\"engine\":\"" + string([]byte{255}) + "\"}}",
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := UnmarshalClientInfo([]byte(raw)); !errors.Is(err, ErrInvalidClientInfo) || out != nil {
				t.Fatalf("accepted %+v %v", out, err)
			}
		})
	}
}

func TestClientInfoExtensionsBoundsAndPrivacy(t *testing.T) {
	for name, raw := range map[string]string{
		"depth four": `{"a":{"b":{"c":1}}}`, "array sixteen": `[` + strings.Repeat("0,", 15) + `0]`,
		"string256": `"` + strings.Repeat("界", 256) + `"`, "primitive": `true`, "null": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeClientInfo(&ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(raw)}}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, raw := range map[string]string{
		"depth five": `{"a":{"b":{"c":{"d":1}}}}`, "array seventeen": `[` + strings.Repeat("0,", 16) + `0]`,
		"string257": `"` + strings.Repeat("界", 257) + `"`, "bad key": `{"Bad":1}`, "duplicate": `{"a":1,"a":2}`,
		"control": `"a\n"`, "bad number": `01`, "empty": ``,
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := NormalizeClientInfo(&ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(raw)}}); !errors.Is(err, ErrInvalidClientInfo) || out != nil {
				t.Fatalf("accepted %+v %v", out, err)
			}
		})
	}
	for _, key := range []string{"password", "api_secret", "access_token", "credential", "authorization", "cookie", "verification_code", "sms_code", "raw_ua", "user_agent", "client_ip", "email_address", "location", "latitude", "longitude", "device_id", "hardware_id", "imei", "mac", "Bad", "a-b", strings.Repeat("a", 65)} {
		for _, raw := range []string{`{"` + key + `":1}`, `[{"` + key + `":1}]`} {
			if _, err := NormalizeClientInfo(&ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(raw)}}); !errors.Is(err, ErrInvalidClientInfo) {
				t.Fatalf("accepted sensitive/invalid nested key %s: %v", key, err)
			}
		}
		if _, err := NormalizeClientInfo(&ClientInfo{Extensions: map[string]json.RawMessage{key: json.RawMessage(`1`)}}); !errors.Is(err, ErrInvalidClientInfo) {
			t.Fatalf("accepted root key %s: %v", key, err)
		}
	}
	// Four arrays plus their 60 elements use exactly 64 nodes.
	ext := map[string]json.RawMessage{}
	for _, key := range []string{"a", "b", "c", "d"} {
		ext[key] = json.RawMessage(`[` + strings.Repeat("0,", 14) + `0]`)
	}
	if _, err := NormalizeClientInfo(&ClientInfo{Extensions: ext}); err != nil {
		t.Fatalf("64 nodes: %v", err)
	}
	ext["e"] = json.RawMessage(`0`)
	if _, err := NormalizeClientInfo(&ClientInfo{Extensions: ext}); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("65 nodes: %v", err)
	}
}

func TestClientInfoCopiesAndEncodedSize(t *testing.T) {
	in := &ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`{"value":1}`)}}
	out, err := NormalizeClientInfo(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Extensions["engine"][9] = '2'
	in.Extensions["other"] = json.RawMessage(`true`)
	if len(out.Extensions) != 1 || string(out.Extensions["engine"]) != `{"value":1}` {
		t.Fatalf("aliased: %+v", out)
	}
	merged := MergeClientInfo(nil, out)
	out.Extensions["engine"][9] = '3'
	if string(merged.Extensions["engine"]) != `{"value":1}` {
		t.Fatal("overlay aliases extensions")
	}
	// HTML escaping expands each '<' to six bytes; text sums alone fit.
	large := &ClientInfo{OSName: strings.Repeat("<", 64), DeviceModel: strings.Repeat("<", 64), DeviceManufacturer: strings.Repeat("<", 64), AppName: strings.Repeat("<", 64), BrowserName: strings.Repeat("<", 64), AppBuild: strings.Repeat("<", 64)}
	if _, err := NormalizeClientInfo(large); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("encoded size: %v", err)
	}
	for _, in := range []*ClientInfo{{DeviceManufacturer: strings.Repeat("a", 65)}, {AppBuild: strings.Repeat("a", 65)}} {
		if _, err := MarshalClientInfo(in); !errors.Is(err, ErrInvalidClientInfo) {
			t.Fatalf("field limit: %v", err)
		}
	}
	version := &ClientInfo{OSVersion: "1", BrowserVersion: "2", AppVersion: "3", AppBuild: "4"}
	encoded, err := MarshalClientInfo(version)
	if err != nil || !bytes.Equal(encoded, []byte(`{"os":{"version":"1"},"app":{"version":"3","build":"4"},"browser":{"version":"2"}}`)) {
		t.Fatalf("version-only: %s %v", encoded, err)
	}
	decoded, err := UnmarshalClientInfo(encoded)
	if err != nil || !reflect.DeepEqual(decoded, version) {
		t.Fatalf("versions lost: %+v %v", decoded, err)
	}
}

func TestMergeClientInfoAppSwitchClearsBuild(t *testing.T) {
	got := MergeClientInfo(&ClientInfo{AppName: "First", AppVersion: "1", AppBuild: "100"}, &ClientInfo{AppName: "Second"})
	if got.AppName != "Second" || got.AppVersion != "" || got.AppBuild != "" {
		t.Fatalf("stale app build: %+v", got)
	}
}

func TestClientInfoJSONEncodedByteBoundary(t *testing.T) {
	ext := map[string]json.RawMessage{}
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		ext[key] = json.RawMessage(`"` + strings.Repeat("x", 250) + `"`)
	}
	ext["h"] = json.RawMessage(`"` + strings.Repeat("x", 226) + `"`)
	raw, err := MarshalClientInfo(&ClientInfo{Extensions: ext})
	if err != nil || len(raw) != 2048 {
		t.Fatalf("2048 bytes: len=%d err=%v", len(raw), err)
	}
	if _, err = UnmarshalClientInfo(raw); err != nil {
		t.Fatalf("decode 2048: %v", err)
	}
	ext["h"] = json.RawMessage(`"` + strings.Repeat("x", 227) + `"`)
	if _, err = MarshalClientInfo(&ClientInfo{Extensions: ext}); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("2049 bytes: %v", err)
	}
	tooLarge := `{"extensions":{`
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		tooLarge += `"` + key + `":"` + strings.Repeat("x", 250) + `",`
	}
	tooLarge += `"h":"` + strings.Repeat("x", 227) + `"}}`
	if _, err = UnmarshalClientInfo([]byte(tooLarge)); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("decode 2049 bytes: %v", err)
	}
}

func TestClientInfoJSONNormalizesNumbers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"1e3", "1000"}, {"1e-3", "0.001"}, {"-12.3400e2", "-1234"}, {"12.3400", "12.34"}, {"1.2300e-2", "0.0123"},
		{"9007199254740993e0", "9007199254740993"}, {"1000e-3", "1"}, {"10e-2", "0.1"},
		{"0e9999999999999999999999999", "0"}, {"-0.000e-9999999999999999999999999", "0"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			info := &ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`{"value":[` + tc.input + `]}`)}}
			raw, err := MarshalClientInfo(info)
			want := `{"extensions":{"engine":{"value":[` + tc.want + `]}}}`
			if err != nil || string(raw) != want {
				t.Fatalf("got=%s err=%v want=%s", raw, err, want)
			}
			decoded, err := UnmarshalClientInfo([]byte(`{"extensions":{"engine":{"value":[` + tc.input + `]}}}`))
			if err != nil || string(decoded.Extensions["engine"]) != `{"value":[`+tc.want+`]}` {
				t.Fatalf("decode=%+v err=%v", decoded, err)
			}
		})
	}
}

func TestClientInfoJSONRejectsExpandedNumbers(t *testing.T) {
	for _, number := range []string{"1e3000", "1e-3000", "1e2147483647", "1e-2147483648", "1e9999999999999999999999999", "1e2027", "1e-2026"} {
		t.Run(number, func(t *testing.T) {
			info := &ClientInfo{Extensions: map[string]json.RawMessage{"n": json.RawMessage(number)}}
			if out, err := NormalizeClientInfo(info); !errors.Is(err, ErrInvalidClientInfo) || out != nil {
				t.Fatalf("normalization accepted %+v %v", out, err)
			}
			if _, err := UnmarshalClientInfo([]byte(`{"extensions":{"n":` + number + `}}`)); !errors.Is(err, ErrInvalidClientInfo) {
				t.Fatalf("decode accepted: %v", err)
			}
		})
	}
	for _, number := range []string{"1e2026", "1e-2025", "-1e2025"} {
		raw, err := MarshalClientInfo(&ClientInfo{Extensions: map[string]json.RawMessage{"n": json.RawMessage(number)}})
		if err != nil || len(raw) != 2048 || bytes.ContainsAny(raw[19:len(raw)-2], "eE") {
			t.Fatalf("2048 decimal boundary %s len=%d err=%v", number, len(raw), err)
		}
	}
}

func TestClientInfoJSONRawInputLimit(t *testing.T) {
	if out, err := UnmarshalClientInfo([]byte(strings.Repeat(" ", 16382) + `{}`)); err != nil || out != nil {
		t.Fatalf("16KiB empty object: %+v %v", out, err)
	}
	if _, err := UnmarshalClientInfo([]byte(strings.Repeat(" ", 16383) + `{}`)); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("accepted over16KiB raw object: %v", err)
	}
	valid := &ClientInfo{Extensions: map[string]json.RawMessage{"n": json.RawMessage(strings.Repeat(" ", 16383) + `1`)}}
	if _, err := NormalizeClientInfo(valid); err != nil {
		t.Fatalf("16KiB extension: %v", err)
	}
	valid.Extensions["n"] = json.RawMessage(strings.Repeat(" ", 16384) + `1`)
	if _, err := NormalizeClientInfo(valid); !errors.Is(err, ErrInvalidClientInfo) {
		t.Fatalf("accepted over16KiB raw extension: %v", err)
	}
}
