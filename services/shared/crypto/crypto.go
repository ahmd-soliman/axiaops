// Package crypto provides AES-256-GCM encryption for storing secrets at rest.
// The encryption key is a 32-byte value read from the ENCRYPTION_KEY env var
// (hex-encoded). Generate one with: openssl rand -hex 32
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns a hex-encoded nonce+ciphertext string safe for DB storage.
func Encrypt(plaintext string) (string, error) {
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
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
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(sealed), nil
}

// Decrypt decrypts a hex-encoded nonce+ciphertext string produced by Encrypt.
func Decrypt(cipherHex string) (string, error) {
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	data, err := hex.DecodeString(cipherHex)
	if err != nil {
		return "", fmt.Errorf("crypto: invalid hex: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("crypto: ciphertext too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt failed: %w", err)
	}
	return string(plain), nil
}

func loadKey() ([]byte, error) {
	raw := os.Getenv("ENCRYPTION_KEY")
	if raw == "" {
		return nil, errors.New("crypto: ENCRYPTION_KEY env var not set")
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: ENCRYPTION_KEY must be hex-encoded: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: ENCRYPTION_KEY must be 32 bytes (got %d)", len(key))
	}
	return key, nil
}
