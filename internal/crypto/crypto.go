package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
)

const (
	keyFileName = "master.key"
	saltSize    = 16
	keySize     = 32
)

// Manager handles encryption of sensitive settings (api_id, api_hash, tokens).
// Key is stored in DataDir/master.key with 0600. If VISNYK_MASTER_PASSWORD env is set,
// key is derived via PBKDF2 from password (for CI/signing).
type Manager struct {
	key []byte
}

// New creates Manager with DataDir. It will load or generate master.key.
func New(dataDir string) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("crypto mkdir: %w", err)
	}
	keyPath := filepath.Join(dataDir, keyFileName)

	// Env override for deterministic builds/tests
	if pwd := os.Getenv("VISNYK_MASTER_PASSWORD"); pwd != "" {
		salt := []byte("visnyk-salt-v1")
		// try load salt from key file if exists to keep stable
		if data, err := os.ReadFile(keyPath); err == nil && len(data) >= saltSize {
			salt = data[:saltSize]
		} else {
			// generate and persist salt
			salt = make([]byte, saltSize)
			if _, err := rand.Read(salt); err != nil {
				return nil, err
			}
			_ = os.WriteFile(keyPath, salt, 0o600)
		}
		key := pbkdf2.Key([]byte(pwd), salt, 100000, keySize, sha256.New)
		return &Manager{key: key}, nil
	}

	// Load or generate random key
	if data, err := os.ReadFile(keyPath); err == nil {
		if len(data) == keySize {
			return &Manager{key: data}, nil
		}
		if len(data) > keySize {
			// legacy: file may contain base64
			if decoded, err2 := base64.StdEncoding.DecodeString(string(data)); err2 == nil && len(decoded) == keySize {
				return &Manager{key: decoded}, nil
			}
		}
	}
	// generate new
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("rand: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	return &Manager{key: key}, nil
}

// NewWithKey creates manager with explicit 32-byte key (for tests).
func NewWithKey(key []byte) (*Manager, error) {
	if len(key) != keySize {
		return nil, errors.New("key must be 32 bytes")
	}
	cp := make([]byte, keySize)
	copy(cp, key)
	return &Manager{key: cp}, nil
}

// Encrypt encrypts plaintext with AES-GCM and returns base64 string.
func (m *Manager) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt decodes base64 and decrypts AES-GCM. If value is not encrypted (no base64 or fail), returns as-is for migration.
func (m *Manager) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		// not encrypted — legacy plain
		return ciphertext, nil
	}
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return ciphertext, nil
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		// decryption failed — maybe plain text stored as base64 by accident
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}

// EncryptBytes for binary data.
func (m *Manager) EncryptBytes(b []byte) (string, error) { return m.Encrypt(string(b)) }

// DecryptBytes returns bytes.
func (m *Manager) DecryptBytes(s string) ([]byte, error) {
	p, err := m.Decrypt(s)
	return []byte(p), err
}

// KeyPath returns path to master key (for docs/debug).
func KeyPath(dataDir string) string { return filepath.Join(dataDir, keyFileName) }
