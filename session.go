package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	"golang.org/x/crypto/hkdf"
)

// Second Oakley Group prime (RFC 2409 section 6.2)
var dhPrime = new(big.Int).SetBytes([]byte{
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	0xC9, 0x0F, 0xDA, 0xA2, 0x21, 0x68, 0xC2, 0x34,
	0xC4, 0xC6, 0x62, 0x8B, 0x80, 0xDC, 0x1C, 0xD1,
	0x29, 0x02, 0x4E, 0x08, 0x8A, 0x67, 0xCC, 0x74,
	0x02, 0x0B, 0xBE, 0xA6, 0x3B, 0x13, 0x9B, 0x22,
	0x51, 0x4A, 0x08, 0x79, 0x8E, 0x34, 0x04, 0xDD,
	0xEF, 0x95, 0x19, 0xB3, 0xCD, 0x3A, 0x43, 0x1B,
	0x30, 0x2B, 0x0A, 0x6D, 0xF2, 0x5F, 0x14, 0x37,
	0x4F, 0xE1, 0x35, 0x6D, 0x6D, 0x51, 0xC2, 0x45,
	0xE4, 0x85, 0xB5, 0x76, 0x62, 0x5E, 0x7E, 0xC6,
	0xF4, 0x4C, 0x42, 0xE9, 0xA6, 0x37, 0xED, 0x6B,
	0x0B, 0xFF, 0x5C, 0xB6, 0xF4, 0x06, 0xB7, 0xED,
	0xEE, 0x38, 0x6B, 0xFB, 0x5A, 0x89, 0x9F, 0xA5,
	0xAE, 0x9F, 0x24, 0x11, 0x7C, 0x4B, 0x1F, 0xE6,
	0x49, 0x28, 0x66, 0x51, 0xEC, 0xE6, 0x53, 0x81,
	0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
})

var dhBase = big.NewInt(2)

// sessionTransfer handles encrypting/decrypting secrets for D-Bus transport
type sessionTransfer interface {
	Encrypt(value []byte, session dbus.ObjectPath) (Secret, error)
	Decrypt(secret Secret) ([]byte, error)
}

// plainTransfer passes secrets through without encryption
type plainTransfer struct{}

func (p *plainTransfer) Encrypt(value []byte, session dbus.ObjectPath) (Secret, error) {
	return Secret{
		Session:     session,
		Parameters:  []byte{},
		Value:       value,
		ContentType: "text/plain",
	}, nil
}

func (p *plainTransfer) Decrypt(secret Secret) ([]byte, error) {
	return secret.Value, nil
}

// dhTransfer handles DH-encrypted secret transport
type dhTransfer struct {
	sharedKey [16]byte // AES-128 key derived via HKDF
}

func (d *dhTransfer) Encrypt(value []byte, session dbus.ObjectPath) (Secret, error) {
	block, err := aes.NewCipher(d.sharedKey[:])
	if err != nil {
		return Secret{}, fmt.Errorf("aes cipher: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return Secret{}, fmt.Errorf("generate iv: %w", err)
	}

	padded := pkcs7Pad(value, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return Secret{
		Session:     session,
		Parameters:  iv,
		Value:       ciphertext,
		ContentType: "text/plain",
	}, nil
}

func (d *dhTransfer) Decrypt(secret Secret) ([]byte, error) {
	block, err := aes.NewCipher(d.sharedKey[:])
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	if len(secret.Parameters) != aes.BlockSize {
		return nil, fmt.Errorf("invalid IV length: %d", len(secret.Parameters))
	}
	if len(secret.Value) == 0 || len(secret.Value)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid ciphertext length: %d", len(secret.Value))
	}

	plaintext := make([]byte, len(secret.Value))
	cipher.NewCBCDecrypter(block, secret.Parameters).CryptBlocks(plaintext, secret.Value)
	return pkcs7Unpad(plaintext)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	pad := make([]byte, padding)
	for i := range pad {
		pad[i] = byte(padding)
	}
	return append(data, pad...)
}

func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding == 0 {
		return nil, fmt.Errorf("invalid padding")
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padding], nil
}

// SessionManager tracks active sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[dbus.ObjectPath]sessionTransfer
	counter  atomic.Uint64
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[dbus.ObjectPath]sessionTransfer),
	}
}

func (sm *SessionManager) Get(path dbus.ObjectPath) (sessionTransfer, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[path]
	return s, ok
}

func (sm *SessionManager) Remove(path dbus.ObjectPath) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, path)
}

func (sm *SessionManager) nextPath() dbus.ObjectPath {
	id := sm.counter.Add(1)
	return dbus.ObjectPath(fmt.Sprintf("%s/session/s%d", servicePath, id))
}

// OpenPlain creates a plain-text session
func (sm *SessionManager) OpenPlain() dbus.ObjectPath {
	path := sm.nextPath()
	sm.mu.Lock()
	sm.sessions[path] = &plainTransfer{}
	sm.mu.Unlock()
	return path
}

// OpenDH creates a DH-encrypted session. Takes client's public key bytes,
// returns (server public key bytes, session path).
func (sm *SessionManager) OpenDH(clientPubKeyBytes []byte) ([]byte, dbus.ObjectPath, error) {
	clientPubKey := new(big.Int).SetBytes(clientPubKeyBytes)

	privKey, err := rand.Int(rand.Reader, dhPrime)
	if err != nil {
		return nil, "", fmt.Errorf("generate private key: %w", err)
	}

	// Compute shared secret: clientPubKey ^ privKey mod prime
	sharedBytes := clientPubKey.Exp(clientPubKey, privKey, dhPrime).Bytes()
	// Pad to 128 bytes
	if len(sharedBytes) < 128 {
		padded := make([]byte, 128)
		copy(padded[128-len(sharedBytes):], sharedBytes)
		sharedBytes = padded
	}

	// Derive AES key via HKDF-SHA256 (no salt, empty info)
	var aesKey [16]byte
	if _, err := hkdf.New(sha256.New, sharedBytes, nil, []byte{}).Read(aesKey[:]); err != nil {
		return nil, "", fmt.Errorf("hkdf: %w", err)
	}

	// Compute server public key: 2 ^ privKey mod prime
	serverPubKey := new(big.Int).Exp(dhBase, privKey, dhPrime)

	path := sm.nextPath()
	sm.mu.Lock()
	sm.sessions[path] = &dhTransfer{sharedKey: aesKey}
	sm.mu.Unlock()

	return serverPubKey.Bytes(), path, nil
}

// Session implements org.freedesktop.Secret.Session
type Session struct {
	manager *SessionManager
	path    dbus.ObjectPath
}

func (s *Session) Close() *dbus.Error {
	s.manager.Remove(s.path)
	return nil
}
