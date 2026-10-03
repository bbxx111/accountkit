package pii

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// Cipher 做 AES-256-GCM 加解密，按版本选钥。nonce 每次随机并前置于密文。
type Cipher struct {
	aeads    map[uint16]cipher.AEAD
	active   uint16
	versions []uint16
}

// NewCipher 校验并构造。active 必须存在于 keys；每个密钥必须恰好 32 字节（AES-256）；
// 不做截断或派生。
func NewCipher(keys map[uint16][]byte, active uint16) (*Cipher, error) {
	copied, versions, err := checkKeys(keys, active)
	if err != nil {
		return nil, err
	}
	aeads := make(map[uint16]cipher.AEAD, len(copied))
	for v, k := range copied {
		if len(k) != 32 {
			return nil, fmt.Errorf("pii: cipher key version %d is %d bytes, AES-256-GCM requires exactly 32", v, len(k))
		}
		block, err := aes.NewCipher(k)
		if err != nil {
			return nil, fmt.Errorf("pii: key version %d: %w", v, err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("pii: key version %d: %w", v, err)
		}
		aeads[v] = gcm
	}
	return &Cipher{aeads: aeads, active: active, versions: versions}, nil
}

// Encrypt 用 active 版本加密，返回 nonce||密文 与版本号。
func (c *Cipher) Encrypt(plain string) ([]byte, uint16, error) {
	gcm := c.aeads[c.active]
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, fmt.Errorf("pii: generating nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), c.active, nil
}

// Decrypt 只用指定版本解密；版本未配置或认证失败即报错，不回退到其他密钥。
func (c *Cipher) Decrypt(ct []byte, version uint16) (string, error) {
	gcm, ok := c.aeads[version]
	if !ok {
		return "", fmt.Errorf("pii: no key configured for version %d", version)
	}
	if len(ct) < gcm.NonceSize() {
		return "", fmt.Errorf("pii: ciphertext shorter than nonce")
	}
	plain, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("pii: decrypt with version %d: %w", version, err)
	}
	return string(plain), nil
}

// Active 返回当前签发用的版本。
func (c *Cipher) Active() uint16 { return c.active }

// Versions 返回全部已配置版本（升序，拷贝）。
func (c *Cipher) Versions() []uint16 { return append([]uint16(nil), c.versions...) }
