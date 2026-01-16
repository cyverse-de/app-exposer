package coordinator

import (
	"bytes"
	"encoding/base64"
	"os"
	"testing"
)

func TestNewEncryptor(t *testing.T) {
	tests := []struct {
		name    string
		keyLen  int
		wantErr bool
	}{
		{"valid 32-byte key", 32, false},
		{"too short key", 16, true},
		{"too long key", 64, true},
		{"empty key", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keyLen)
			_, err := NewEncryptor(key)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncryptor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewEncryptorFromBase64(t *testing.T) {
	// Generate a valid key
	validKey := make([]byte, 32)
	for i := range validKey {
		validKey[i] = byte(i)
	}
	validEncoded := base64.StdEncoding.EncodeToString(validKey)

	tests := []struct {
		name       string
		encodedKey string
		wantErr    bool
	}{
		{"valid base64 key", validEncoded, false},
		{"invalid base64", "not-valid-base64!!!", true},
		{"wrong size after decode", base64.StdEncoding.EncodeToString([]byte("short")), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEncryptorFromBase64(tt.encodedKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncryptorFromBase64() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewEncryptorFromEnv(t *testing.T) {
	// Generate a valid key
	validKey := make([]byte, 32)
	for i := range validKey {
		validKey[i] = byte(i)
	}
	validEncoded := base64.StdEncoding.EncodeToString(validKey)

	tests := []struct {
		name    string
		envVal  string
		setEnv  bool
		wantErr bool
	}{
		{"valid env key", validEncoded, true, false},
		{"no env var", "", false, true},
		{"invalid env key", "invalid", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				os.Setenv(EncryptionKeyEnvVar, tt.envVal)
				defer os.Unsetenv(EncryptionKeyEnvVar)
			} else {
				os.Unsetenv(EncryptionKeyEnvVar)
			}

			_, err := NewEncryptorFromEnv()
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEncryptorFromEnv() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor() error = %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", []byte{}},
		{"short text", []byte("hello")},
		{"longer text", []byte("this is a longer piece of text that needs to be encrypted")},
		{"binary data", []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0xfd}},
		{"PEM-like content", []byte("-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhki...\n-----END PRIVATE KEY-----")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			// Ciphertext should be longer than plaintext (nonce + tag)
			if len(ciphertext) <= len(tt.plaintext) {
				t.Errorf("Ciphertext length %d should be > plaintext length %d", len(ciphertext), len(tt.plaintext))
			}

			decrypted, err := enc.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			if !bytes.Equal(decrypted, tt.plaintext) {
				t.Errorf("Decrypted data doesn't match original.\nGot: %v\nWant: %v", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	plaintext := []byte("same plaintext")

	ciphertext1, _ := enc.Encrypt(plaintext)
	ciphertext2, _ := enc.Encrypt(plaintext)

	// Same plaintext should produce different ciphertext due to random nonce
	if bytes.Equal(ciphertext1, ciphertext2) {
		t.Error("Encrypting same plaintext twice should produce different ciphertext")
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()

	enc1, _ := NewEncryptor(key1)
	enc2, _ := NewEncryptor(key2)

	plaintext := []byte("secret data")
	ciphertext, _ := enc1.Encrypt(plaintext)

	// Try to decrypt with wrong key
	_, err := enc2.Decrypt(ciphertext)
	if err == nil {
		t.Error("Decrypt with wrong key should fail")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	plaintext := []byte("secret data")
	ciphertext, _ := enc.Encrypt(plaintext)

	// Tamper with ciphertext
	ciphertext[len(ciphertext)-1] ^= 0xff

	_, err := enc.Decrypt(ciphertext)
	if err == nil {
		t.Error("Decrypt tampered ciphertext should fail")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	// Try to decrypt data shorter than nonce
	_, err := enc.Decrypt([]byte{1, 2, 3})
	if err != ErrCiphertextTooShort {
		t.Errorf("Expected ErrCiphertextTooShort, got %v", err)
	}
}

func TestEncryptDecryptString(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	original := "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC...\n-----END PRIVATE KEY-----"

	encrypted, err := enc.EncryptString(original)
	if err != nil {
		t.Fatalf("EncryptString() error = %v", err)
	}

	// Result should be valid base64
	_, err = base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		t.Errorf("Encrypted string should be valid base64: %v", err)
	}

	decrypted, err := enc.DecryptString(encrypted)
	if err != nil {
		t.Fatalf("DecryptString() error = %v", err)
	}

	if decrypted != original {
		t.Errorf("DecryptString() = %v, want %v", decrypted, original)
	}
}

func TestDecryptStringInvalidBase64(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptor(key)

	_, err := enc.DecryptString("not-valid-base64!!!")
	if err == nil {
		t.Error("DecryptString with invalid base64 should fail")
	}
}

func TestGenerateKey(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	if len(key) != AES256KeySize {
		t.Errorf("GenerateKey() returned key of length %d, want %d", len(key), AES256KeySize)
	}

	// Generate another key, should be different
	key2, _ := GenerateKey()
	if bytes.Equal(key, key2) {
		t.Error("GenerateKey() should produce different keys each time")
	}
}

func TestGenerateKeyBase64(t *testing.T) {
	encoded, err := GenerateKeyBase64()
	if err != nil {
		t.Fatalf("GenerateKeyBase64() error = %v", err)
	}

	// Should be valid base64
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Errorf("GenerateKeyBase64() should return valid base64: %v", err)
	}

	if len(decoded) != AES256KeySize {
		t.Errorf("GenerateKeyBase64() decoded length = %d, want %d", len(decoded), AES256KeySize)
	}
}
