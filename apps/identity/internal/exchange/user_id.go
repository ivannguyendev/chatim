package exchange

import (
	"encoding/base32"
	"fmt"
	"io"
	"strings"
)

const userIDBytes = 16

var userIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newUserID(random io.Reader) (string, error) {
	b := make([]byte, userIDBytes)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", fmt.Errorf("read random user id: %w", err)
	}
	return strings.ToLower(userIDEncoding.EncodeToString(b)), nil
}
