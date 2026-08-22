package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func GenerateHMACWithKey(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func GenerateHMAC(data string) string {
	h := hmac.New(sha256.New, []byte(CryptoSecret))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func Password2Hash(password string) (string, error) {
	passwordBytes := []byte(password)
	hashedPassword, err := bcrypt.GenerateFromPassword(passwordBytes, bcrypt.DefaultCost)
	return string(hashedPassword), err
}

func ValidatePasswordAndHash(password string, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// cipherPrefixV1 marks AES-GCM ciphertext encoded with EncryptSecret.
// Format: "enc:v1:" + base64(nonce(12B) || ciphertext || tag)
const cipherPrefixV1 = "enc:v1:"

// ErrEmptyCipher 解密时传入了空串。
var ErrEmptyCipher = errors.New("empty ciphertext")

func deriveAESKey(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

// EncryptSecret 使用 CryptoSecret 派生 AES-256-GCM 密钥加密任意明文。
// 已带 enc:v1: 前缀的串视为已加密，原样返回（幂等），便于反复 Upsert。
func EncryptSecret(plain string) (string, error) {
	if strings.HasPrefix(plain, cipherPrefixV1) {
		return plain, nil
	}
	key := deriveAESKey(CryptoSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	buf := make([]byte, 0, len(nonce)+len(ct))
	buf = append(buf, nonce...)
	buf = append(buf, ct...)
	return cipherPrefixV1 + base64.StdEncoding.EncodeToString(buf), nil
}

// DecryptSecret 与 EncryptSecret 对应；不带 enc:v1: 前缀视为明文兼容（迁移期）。
func DecryptSecret(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", ErrEmptyCipher
	}
	if !strings.HasPrefix(ciphertext, cipherPrefixV1) {
		return ciphertext, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, cipherPrefixV1))
	if err != nil {
		return "", err
	}
	key := deriveAESKey(CryptoSecret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize+1 {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// IsEncryptedSecret 判断字符串是否已是 EncryptSecret 输出格式。
func IsEncryptedSecret(s string) bool {
	return strings.HasPrefix(s, cipherPrefixV1)
}
