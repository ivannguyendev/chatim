package directory

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const (
	MaxNameRunes   = 128
	MaxAvatarBytes = 2048
)

var (
	ErrDisplayName = fmt.Errorf("%w: display name must be 1 to %d characters of valid text", apperr.ErrInvalidArgument, MaxNameRunes)
	ErrGroupName   = fmt.Errorf("%w: group name must be 1 to %d characters of valid text", apperr.ErrInvalidArgument, MaxNameRunes)
	ErrAvatarURL   = fmt.Errorf("%w: avatar url must be empty or an https url with a host, no user info, at most %d bytes", apperr.ErrInvalidArgument, MaxAvatarBytes)
	ErrGroupID     = fmt.Errorf("%w: group id must be 1 to %d bytes, each a letter, digit, underscore, hyphen or colon", apperr.ErrInvalidArgument, keys.MaxMentionID)
)

func NormalizeDisplayName(raw string) (string, error) {
	return normalizeName(raw, ErrDisplayName)
}

func NormalizeGroupName(raw string) (string, error) {
	return normalizeName(raw, ErrGroupName)
}

func ValidateAvatarURL(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > MaxAvatarBytes {
		return ErrAvatarURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.Join(ErrAvatarURL, err)
	}
	if u.Scheme != "https" || u.Opaque != "" || u.Hostname() == "" || u.User != nil {
		return ErrAvatarURL
	}
	return nil
}

func ValidateGroupID(id string) error {
	if !keys.ValidMentionID(id) {
		return ErrGroupID
	}
	return nil
}

func normalizeName(raw string, invalid error) (string, error) {
	if !utf8.ValidString(raw) {
		return "", invalid
	}
	name := strings.TrimSpace(strings.Map(dropHidden, raw))
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameRunes {
		return "", invalid
	}
	return name, nil
}

func dropHidden(r rune) rune {
	if unicode.IsControl(r) || bidiControl(r) {
		return -1
	}
	return r
}

func bidiControl(r rune) bool {
	switch {
	case r == '\u200e', r == '\u200f':
		return true
	case r >= '\u202a' && r <= '\u202e':
		return true
	default:
		return r >= '\u2066' && r <= '\u2069'
	}
}
