// Package crypto 提供 AES-256-GCM 加解密。全服务只有 CREDENTIAL_MASTER_KEY 一把
// 加密主密钥，平台渠道的 API Key 与后续需要加密落库的敏感字段都复用它。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrKeyInvalid 表示主密钥长度不符合 AES-256 要求。
var ErrKeyInvalid = errors.New("CREDENTIAL_MASTER_KEY 必须为 32 字节")

// Cipher 持有派生自主密钥的 AEAD。GCM 的 nonce 长度为 12 字节。
type Cipher struct {
	aead cipher.AEAD
}

// New 依据 32 字节主密钥构造加密器。
func New(key string) (*Cipher, error) {
	if len(key) != 32 {
		return nil, ErrKeyInvalid
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文，返回 nonce 与密文（含认证标签）。
func (c *Cipher) Encrypt(plaintext []byte) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("生成 nonce 失败: %w", err)
	}
	return nonce, c.aead.Seal(nil, nonce, plaintext, nil), nil
}

// Decrypt 解密 ciphertext，nonce 必须与加密时返回的一致。
func (c *Cipher) Decrypt(nonce, ciphertext []byte) ([]byte, error) {
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("解密失败：密文被篡改或主密钥不匹配")
	}
	return plaintext, nil
}

// Mask 返回密钥的展示用形态：只保留后四位，其余用星号替代。
// 管理接口永远不返回明文，只返回这个掩码与 hasKey。
func Mask(secret string) string {
	runes := []rune(secret)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

// EncodeBase64 用于把密钥以字符串形式提交给上游请求头。
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
