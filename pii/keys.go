// Package pii 提供存储个人信息用的版本化密码学原语：AES-256-GCM 认证加密
// （明文回显路径）与带密钥的 HMAC-SHA256 摘要（等值查找路径）。
//
// Cipher 与 Digester 都严格按版本选钥，没有"逐个密钥试解"的回退：版本不匹配
// 是硬错误。密钥轮换靠版本列表 + active 版本完成：新版本先加入列表，切换 active，
// 后台任务重算旧行，最后移除旧版本。
package pii

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// MinKeyLen 是密钥最小长度（字节）。AES-256 与 HMAC-SHA256 的安全性取决于密钥熵，
// 短密钥直接拒绝而不是靠运维自觉。
const MinKeyLen = 32

// ParseKeyList 解析 "version:base64,version:base64" 形式的密钥列表。
// version 为 1..65535，唯一；base64 为标准编码（带 padding 亦可）；解码后 ≥ MinKeyLen。
func ParseKeyList(spec string) (map[uint16][]byte, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("pii: key list is empty")
	}
	out := map[uint16][]byte{}
	for i, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		vStr, b64, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("pii: key item #%d is not version:base64", i+1)
		}
		v64, err := strconv.ParseUint(strings.TrimSpace(vStr), 10, 16)
		if err != nil || v64 == 0 {
			return nil, fmt.Errorf("pii: key version %q must be an integer in 1..65535", vStr)
		}
		v := uint16(v64)
		if _, dup := out[v]; dup {
			return nil, fmt.Errorf("pii: duplicate key version %d", v)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(b64))
		}
		if err != nil {
			return nil, fmt.Errorf("pii: key version %d is not valid base64", v)
		}
		if len(raw) < MinKeyLen {
			return nil, fmt.Errorf("pii: key version %d is %d bytes, want at least %d", v, len(raw), MinKeyLen)
		}
		out[v] = raw
	}
	return out, nil
}

// checkKeys 校验非空、长度、active 存在，并返回拷贝后的密钥与升序版本列表。
func checkKeys(keys map[uint16][]byte, active uint16) (map[uint16][]byte, []uint16, error) {
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("pii: no keys provided")
	}
	copied := make(map[uint16][]byte, len(keys))
	versions := make([]uint16, 0, len(keys))
	for v, k := range keys {
		if len(k) < MinKeyLen {
			return nil, nil, fmt.Errorf("pii: key version %d is %d bytes, want at least %d", v, len(k), MinKeyLen)
		}
		copied[v] = append([]byte(nil), k...)
		versions = append(versions, v)
	}
	if _, ok := copied[active]; !ok {
		return nil, nil, fmt.Errorf("pii: active version %d not found among configured keys", active)
	}
	sortVersions(versions)
	return copied, versions, nil
}

func sortVersions(v []uint16) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1] > v[j]; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}
