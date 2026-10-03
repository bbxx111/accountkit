package idp

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// parseRSAJWKSet 解析 JWKS，只保留 kty=RSA 且 kid 非空的键（Apple 只发 RSA）。
func parseRSAJWKSet(data []byte) (map[string]*rsa.PublicKey, error) {
	var set jwkSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	out := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		// use/alg 若声明了就必须是签名用的 RS256；留空视为未声明（Apple 历来不带这两个
		// 字段），但一旦声明就不能是别的用途/算法（例如 "enc" 或 "RS384"）。
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if k.Alg != "" && k.Alg != "RS256" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("jwks: key %s: n: %w", k.Kid, err)
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("jwks: key %s: e: %w", k.Kid, err)
		}
		if len(n) < 256 { // < 2048 bit
			return nil, fmt.Errorf("jwks: key %s: modulus too small", k.Kid)
		}
		if len(n) > 1024 { // > 8192 bit：明显不合理，拒绝而不是花巨大代价构造公钥
			return nil, fmt.Errorf("jwks: key %s: modulus too large", k.Kid)
		}
		eInt := new(big.Int).SetBytes(e)
		if !eInt.IsInt64() || eInt.Int64() < 3 || eInt.Int64() > 1<<31-1 {
			return nil, fmt.Errorf("jwks: key %s: bad exponent", k.Kid)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(eInt.Int64())}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks: no RSA keys")
	}
	return out, nil
}
