package signing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
)

const KEKLength = 32

var ErrSealedKey = errors.New("sealed signing key cannot be opened")

type Sealer struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewSealer(kek []byte, random io.Reader) (*Sealer, error) {
	if len(kek) != KEKLength {
		return nil, fmt.Errorf("key encryption key must be %d bytes", KEKLength)
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Sealer{aead: aead, random: random}, nil
}

func (s *Sealer) Seal(k Key) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, k.Private.Seed(), []byte(k.ID)), nil
}

func (s *Sealer) Open(id string, sealed []byte) (Key, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return Key{}, ErrSealedKey
	}
	seed, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(id))
	if err != nil || len(seed) != ed25519.SeedSize {
		return Key{}, ErrSealedKey
	}
	return Key{ID: id, Private: ed25519.NewKeyFromSeed(seed)}, nil
}
