package signing

import (
	"crypto/ed25519"
	"encoding/base32"
	"fmt"
	"io"
	"strings"
)

const (
	keyIDPrefix     = "k-"
	keyIDRandomSize = 8
	keyIDSuffixSize = 12
)

var keyIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type Key struct {
	ID      string
	Private ed25519.PrivateKey
}

type PublicKey struct {
	ID  string
	Key ed25519.PublicKey
}

func Generate(random io.Reader) (Key, error) {
	idBytes := make([]byte, keyIDRandomSize)
	if _, err := io.ReadFull(random, idBytes); err != nil {
		return Key{}, fmt.Errorf("read key id: %w", err)
	}
	_, private, err := ed25519.GenerateKey(random)
	if err != nil {
		return Key{}, fmt.Errorf("generate ed25519 key: %w", err)
	}
	suffix := strings.ToLower(keyIDEncoding.EncodeToString(idBytes))[:keyIDSuffixSize]
	return Key{ID: keyIDPrefix + suffix, Private: private}, nil
}

func (k Key) PublicKey() PublicKey {
	public, _ := k.Private.Public().(ed25519.PublicKey)
	return PublicKey{ID: k.ID, Key: public}
}
