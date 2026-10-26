package session

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	IDLength     = 26
	SecretLength = 43
	idBytes      = 16
	secretBytes  = 32
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewID(random io.Reader) (string, error) {
	b := make([]byte, idBytes)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("read id: %w", err)
	}
	return strings.ToLower(idEncoding.EncodeToString(b)), nil
}

func NewRefreshToken(sid string, random io.Reader) (string, error) {
	b := make([]byte, secretBytes)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("read refresh secret: %w", err)
	}
	return sid + "." + base64.RawURLEncoding.EncodeToString(b), nil
}

func ParseRefreshToken(token string) (string, bool) {
	sid, secret, ok := strings.Cut(token, ".")
	if !ok || !ValidID(sid) || len(secret) != SecretLength {
		return "", false
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(secret); err != nil {
		return "", false
	}
	return sid, true
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func ValidID(s string) bool {
	if len(s) != IDLength {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}
