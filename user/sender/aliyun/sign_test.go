package aliyun

import (
	"net/url"
	"testing"
)

// TestSignOfficialVector 使用阿里云官方文档“V2 版本 RPC 风格 API 请求的签名机制”中的完整示例
// （https://help.aliyun.com/zh/sdk/product-overview/rpc-mechanism）作为固定向量。
func TestSignOfficialVector(t *testing.T) {
	params := url.Values{
		"AccessKeyId":      {"testid"},
		"Action":           {"DescribeDedicatedHosts"},
		"Format":           {"JSON"},
		"RegionId":         {"cn-beijing"},
		"SignatureMethod":  {"HMAC-SHA1"},
		"SignatureNonce":   {"edb2b34af0af9a6d14deaf7c1a5315eb"},
		"SignatureVersion": {"1.0"},
		"Timestamp":        {"2023-03-13T08:34:30Z"},
		"Version":          {"2014-05-26"},
	}
	if got := sign("GET", "testsecret", params); got != "9NaGiOspFP5UPcwX8Iwt2YJXXuk=" {
		t.Fatalf("signature = %q", got)
	}
}

func TestPercentEncode(t *testing.T) {
	for in, want := range map[string]string{
		"a b":          "a%20b",
		"*":            "%2A",
		"~-_.":         "~-_.",
		"{\"code\":1}": "%7B%22code%22%3A1%7D",
		"食方":           "%E9%A3%9F%E6%96%B9",
	} {
		if got := percentEncode(in); got != want {
			t.Errorf("percentEncode(%q) = %q, want %q", in, got, want)
		}
	}
}
