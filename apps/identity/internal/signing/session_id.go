package signing

import (
	"encoding/base32"
	"fmt"
	"io"
	"strings"
)

const (
	sessionIDLength = 26
	sessionIDBytes  = 16
)

var sessionIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func NewSessionID(random io.Reader) (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("read session id: %w", err)
	}
	return strings.ToLower(sessionIDEncoding.EncodeToString(b)), nil
}

func ValidSessionID(s string) bool {
	if len(s) != sessionIDLength {
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
