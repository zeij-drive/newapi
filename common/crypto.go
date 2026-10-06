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
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// EncryptWithCryptoSecret encrypts configuration secrets with the same
// process-wide CRYPTO_SECRET used by the rest of new-api. The payload is
// versioned so the format can evolve without exposing plaintext in storage.
func EncryptWithCryptoSecret(plaintext string) (string, error) {
	key := sha256.Sum256([]byte(CryptoSecret))
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
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte("new-api-config-v1"))
	payload := append(nonce, ciphertext...)
	return "v1." + base64.RawStdEncoding.EncodeToString(payload), nil
}

func DecryptWithCryptoSecret(payload string) (string, error) {
	if !strings.HasPrefix(payload, "v1.") {
		return "", errors.New("invalid encrypted config payload")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(payload, "v1."))
	if err != nil {
		return "", fmt.Errorf("decode encrypted config: %w", err)
	}
	key := sha256.Sum256([]byte(CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted config payload")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("new-api-config-v1"))
	if err != nil {
		return "", errors.New("invalid encrypted config payload")
	}
	return string(plaintext), nil
}

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
	if strings.HasPrefix(hash, "$argon2id$") {
		return validateArgon2AccountPassword(password, hash)
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
