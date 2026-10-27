package tokens

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/bbxx111/accountkit/pii"
)

// ParsePrivateKey 解析 PKCS#8 DER，只接受 P-256 ECDSA 私钥；错误不包含输入内容。
func ParsePrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("tokens: key is not PKCS#8 DER")
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, errors.New("tokens: key must be a P-256 ECDSA private key")
	}
	return ec, nil
}

// ParseKeyList 解析 "version:base64(PKCS#8 DER),..." 形式的签名密钥列表（JWT_KEYS）。
func ParseKeyList(spec string) (map[uint16]*ecdsa.PrivateKey, error) {
	raw, err := pii.ParseKeyList(spec)
	if err != nil {
		return nil, err
	}
	out := make(map[uint16]*ecdsa.PrivateKey, len(raw))
	for v, der := range raw {
		k, err := ParsePrivateKey(der)
		if err != nil {
			return nil, fmt.Errorf("key version %d: %w", v, err)
		}
		out[v] = k
	}
	return out, nil
}
