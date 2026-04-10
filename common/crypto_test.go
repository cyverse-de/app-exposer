package common

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32) // AES-256
	_, err := rand.Read(key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecrypt(t *testing.T) {
	key := generateTestKey(t)

	tests := []struct {
		name      string
		plaintext string
	}{
		{"empty string", ""},
		{"short string", "hello"},
		{"password-like", "s3cret!P@ssw0rd"},
		{"unicode", "こんにちは世界"},
		{"long string", "the quick brown fox jumps over the lazy dog repeatedly many times"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := Encrypt(tt.plaintext, key)
			require.NoError(t, err)

			assert.NotEqual(t, tt.plaintext, encrypted, "ciphertext should differ from plaintext")

			decrypted, err := Decrypt(encrypted, key)
			require.NoError(t, err)
			assert.Equal(t, tt.plaintext, decrypted)
		})
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := generateTestKey(t)
	plain := "same input"

	c1, err := Encrypt(plain, key)
	require.NoError(t, err)

	c2, err := Encrypt(plain, key)
	require.NoError(t, err)

	assert.NotEqual(t, c1, c2, "AES-GCM with random nonce should produce different ciphertexts")
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)

	encrypted, err := Encrypt("secret", key1)
	require.NoError(t, err)

	_, err = Decrypt(encrypted, key2)
	assert.Error(t, err)
}

func TestEncryptBadKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"not base64", "not-valid-base64!!!"},
		{"too short", base64.StdEncoding.EncodeToString([]byte("short"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Encrypt("hello", tt.key)
			assert.Error(t, err)
		})
	}
}

func TestDecryptBadInput(t *testing.T) {
	key := generateTestKey(t)

	tests := []struct {
		name       string
		ciphertext string
	}{
		{"not base64", "not-valid-base64!!!"},
		{"too short", base64.StdEncoding.EncodeToString([]byte("x"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decrypt(tt.ciphertext, key)
			assert.Error(t, err)
		})
	}
}
