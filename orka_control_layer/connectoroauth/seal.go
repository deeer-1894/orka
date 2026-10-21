package connectoroauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// Sealer encrypts grant secrets at rest with AES-256-GCM. The stored form is
// "v1:" + base64(nonce || ciphertext); anything else is refused, so a token
// written in the clear can never be mistaken for a sealed one.
type Sealer struct{ aead cipher.AEAD }

// NewSealer derives the key from secret (CONNECTOR_SECRET, or the deployment's
// session secret when that is unset), domain-separated so it never equals a
// key used for anything else.
func NewSealer(secret string) (*Sealer, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("connector token key is empty")
	}
	key := sha256.Sum256([]byte("orka/connector-tokens/v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(s.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Sealer) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return "", errors.New("connector secret is not sealed")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(data) < s.aead.NonceSize() {
		return "", errors.New("connector secret is corrupt")
	}
	plain, err := s.aead.Open(nil, data[:s.aead.NonceSize()], data[s.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("connector secret cannot be opened")
	}
	return string(plain), nil
}
