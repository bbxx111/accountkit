package aliyun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"sort"
	"strings"
)

// percentEncode 按阿里云 RPC 签名规则编码：保留 A-Za-z0-9-_.~，空格为 %20，* 为 %2A。
func percentEncode(s string) string {
	return strings.NewReplacer("+", "%20", "*", "%2A", "%7E", "~").Replace(url.QueryEscape(s))
}

// sign 计算 RPC 风格（SignatureVersion 1.0，HMAC-SHA1）签名：
// Base64(HMAC-SHA1(secret+"&", method&%2F&percentEncode(按键排序的规范化查询串)))。params 不含 Signature。
func sign(method, secret string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "Signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, percentEncode(k)+"="+percentEncode(params.Get(k)))
	}
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(method + "&" + percentEncode("/") + "&" + percentEncode(strings.Join(pairs, "&"))))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
