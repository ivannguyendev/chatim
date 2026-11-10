package exchange

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestNewUserIDIsLowercaseBase32OfSixteenBytes(t *testing.T) {
	for name, tc := range map[string]struct {
		random []byte
		want   string
	}{
		"zero bytes":               {bytes.Repeat([]byte{0}, 16), strings.Repeat("a", 26)},
		"all ones":                 {bytes.Repeat([]byte{0xff}, 16), strings.Repeat("7", 25) + "4"},
		"extra bytes are not read": {append(bytes.Repeat([]byte{0}, 16), 0xff), strings.Repeat("a", 26)},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := newUserID(bytes.NewReader(tc.random)); err != nil || got != tc.want {
				t.Fatalf("newUserID = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestNewUserIDFromCryptoRandIsAValidUserID(t *testing.T) {
	first, err := newUserID(rand.Reader)
	if err != nil || len(first) != 26 || ids.ValidUser(first) != nil || strings.ToLower(first) != first {
		t.Fatalf("newUserID = %q, %v; want a valid 26-char lowercase id", first, err)
	}
	if second, err := newUserID(rand.Reader); err != nil || second == first {
		t.Fatalf("second newUserID = %q, %v; want a different id", second, err)
	}
}

func TestNewUserIDFailsOnAShortRead(t *testing.T) {
	if got, err := newUserID(bytes.NewReader(make([]byte, 15))); !errors.Is(err, io.ErrUnexpectedEOF) || got != "" {
		t.Fatalf("newUserID = %q, %v; want an error", got, err)
	}
}
