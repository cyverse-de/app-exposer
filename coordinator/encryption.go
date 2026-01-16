package coordinator

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	// AES256KeySize is the required key size for AES-256
	AES256KeySize = 32

	// NonceSize is the size of the GCM nonce
	NonceSize = 12

	// EncryptionKeyEnvVar is the environment variable for the encryption key
	EncryptionKeyEnvVar = "APP_EXPOSER_ENCRYPTION_KEY"
)

var (
	// ErrInvalidKeySize is returned when the key is not 32 bytes
	ErrInvalidKeySize = errors.New("encryption key must be exactly 32 bytes")

	// ErrCiphertextTooShort is returned when ciphertext is shorter than nonce
	ErrCiphertextTooShort = errors.New("ciphertext too short")

	// ErrNoEncryptionKey is returned when no encryption key is configured
	ErrNoEncryptionKey = errors.New("no encryption key configured")
)

// Encryptor handles encryption and decryption of sensitive data.
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new Encryptor with the given key.
// The key must be exactly 32 bytes for AES-256.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != AES256KeySize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKeySize, len(key))
	}
	return &Encryptor{key: key}, nil
}

// NewEncryptorFromBase64 creates a new Encryptor from a base64-encoded key.
func NewEncryptorFromBase64(encodedKey string) (*Encryptor, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 key: %w", err)
	}
	return NewEncryptor(key)
}

// NewEncryptorFromEnv creates a new Encryptor using the key from the
// APP_EXPOSER_ENCRYPTION_KEY environment variable (base64-encoded).
func NewEncryptorFromEnv() (*Encryptor, error) {
	encodedKey := os.Getenv(EncryptionKeyEnvVar)
	if encodedKey == "" {
		return nil, ErrNoEncryptionKey
	}
	return NewEncryptorFromBase64(encodedKey)
}

// Encrypt encrypts plaintext using AES-256-GCM.
// Returns: nonce (12 bytes) || ciphertext || tag (16 bytes)
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext and tag to the nonce
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext that was encrypted with Encrypt.
// Expects format: nonce (12 bytes) || ciphertext || tag (16 bytes)
func (e *Encryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < NonceSize {
		return nil, ErrCiphertextTooShort
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := ciphertext[:NonceSize]
	ciphertext = ciphertext[NonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// EncryptString encrypts a string and returns base64-encoded ciphertext.
func (e *Encryptor) EncryptString(plaintext string) (string, error) {
	ciphertext, err := e.Encrypt([]byte(plaintext))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString decrypts base64-encoded ciphertext and returns the plaintext string.
func (e *Encryptor) DecryptString(encodedCiphertext string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encodedCiphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 ciphertext: %w", err)
	}
	plaintext, err := e.Decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// GenerateKey generates a random 32-byte key suitable for AES-256.
func GenerateKey() ([]byte, error) {
	key := make([]byte, AES256KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}

// GenerateKeyBase64 generates a random key and returns it base64-encoded.
func GenerateKeyBase64() (string, error) {
	key, err := GenerateKey()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
