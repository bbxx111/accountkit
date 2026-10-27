// Package testkeys 为本模块测试提供按序号确定的合成 ES256 签名密钥，不用于生产。
//
// 同一序号在不同进程中得到相同密钥：恢复演练等多进程测试依赖跨进程一致的签名密钥。
package testkeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"sync"
)

var (
	mu   sync.Mutex
	keys = map[int]*ecdsa.PrivateKey{}
)

// JWT 返回序号 i 对应的 P-256 私钥；私钥标量由序号经 SHA-256 确定性派生，不同序号互不相同。
func JWT(i int) *ecdsa.PrivateKey {
	mu.Lock()
	defer mu.Unlock()
	if k, ok := keys[i]; ok {
		return k
	}
	seed := sha256.Sum256(fmt.Appendf(nil, "accountkit-synthetic-test-jwt-key-%d", i))
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), seed[:])
	if err != nil {
		panic(err)
	}
	keys[i] = k
	return k
}

// JWTBase64 返回 JWT(i) 的 PKCS#8 DER 标准 base64，即 JWT_KEYS 环境变量中单个版本的值。
func JWTBase64(i int) string {
	der, err := x509.MarshalPKCS8PrivateKey(JWT(i))
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
