// Package testkeys 为本模块测试提供按序号缓存的合成 ES256 签名密钥，不用于生产。
package testkeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"sync"
)

var (
	mu   sync.Mutex
	keys = map[int]*ecdsa.PrivateKey{}
)

// JWT 返回序号 i 对应的 P-256 私钥；同一进程内同一序号返回同一密钥，不同序号互不相同。
func JWT(i int) *ecdsa.PrivateKey {
	mu.Lock()
	defer mu.Unlock()
	if k, ok := keys[i]; ok {
		return k
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
