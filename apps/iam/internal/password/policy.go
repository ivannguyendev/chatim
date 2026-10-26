package password

import (
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MaxLength         = 128
	TemporaryLength   = 20
	TemporaryAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type Policy struct {
	MinLength int
}

func (p Policy) Check(password string) error {
	if !utf8.ValidString(password) {
		return fmt.Errorf("%w: password must be valid utf-8", apperr.ErrInvalidArgument)
	}
	n := utf8.RuneCountInString(password)
	if n < p.MinLength || n > MaxLength {
		return fmt.Errorf("%w: password must be %d to %d characters", apperr.ErrInvalidArgument, p.MinLength, MaxLength)
	}
	return nil
}

func Temporary(random io.Reader) (string, error) {
	limit := byte(256 - 256%len(TemporaryAlphabet))
	out := make([]byte, 0, TemporaryLength)
	buf := make([]byte, TemporaryLength)
	for len(out) < TemporaryLength {
		if _, err := io.ReadFull(random, buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		for _, b := range buf {
			if b >= limit || len(out) == TemporaryLength {
				continue
			}
			out = append(out, TemporaryAlphabet[int(b)%len(TemporaryAlphabet)])
		}
	}
	return string(out), nil
}
