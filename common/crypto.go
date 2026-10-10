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

// ConfigEncryptionSecret is initialized from the internal database store before
// options are loaded. It is independent of session and cache signing secrets.
var ConfigEncryptionSecret string

// EncryptWithCryptoSecret writes v2 configuration ciphertext with the persisted
// secret, so restarts and environment-variable changes do not lose saved keys.
func EncryptWithCryptoSecret(plaintext string) (string, error) {
	if ConfigEncryptionSecret == "" {
		return "", errors.New("configuration encryption is not initialized")
	}
	key := sha256.Sum256([]byte(ConfigEncryptionSecret))
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
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte("new-api-config-v2"))
	payload := append(nonce, ciphertext...)
	return "v2." + base64.RawStdEncoding.EncodeToString(payload), nil
}

func DecryptWithCryptoSecret(payload string) (string, error) {
	version, encoded, ok := strings.Cut(payload, ".")
	secret := CryptoSecret
	switch version {
	case "v1":
		// Earlier versions used the deployment's CRYPTO_SECRET or SESSION_SECRET.
	case "v2":
		secret = ConfigEncryptionSecret
	default:
		return "", errors.New("invalid encrypted config payload")
	}
	if !ok || secret == "" {
		return "", errors.New("invalid encrypted config payload")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted config: %w", err)
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted config payload")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte("new-api-config-"+version))
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
