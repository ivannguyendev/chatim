package password

import (
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const dummyLength = 32

type Hasher struct {
	params Params
	random io.Reader
	dummy  string
}

func NewHasher(p Params, random io.Reader) (*Hasher, error) {
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("argon2id params: %w", err)
	}
	h := &Hasher{params: p, random: random}
	seed := make([]byte, dummyLength)
	if _, err := io.ReadFull(random, seed); err != nil {
		return nil, fmt.Errorf("read dummy seed: %w", err)
	}
	dummy, err := h.Hash(hex.EncodeToString(seed))
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := derive(h.params, password, salt)
	return encode(h.params, salt, key), nil
}

func (h *Hasher) Verify(encoded, password string) (bool, error) {
	d, err := decode(encoded)
	if err != nil {
		return false, err
	}
	key := derive(d.params, password, d.salt)
	return subtle.ConstantTimeCompare(key, d.key) == 1, nil
}

func (h *Hasher) VerifyDummy(password string) {
	_, _ = h.Verify(h.dummy, password)
}

func (h *Hasher) NeedsRehash(encoded string) bool {
	d, err := decode(encoded)
	if err != nil {
		return true
	}
	return d.params != h.params
}

func derive(p Params, password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLength)
}
