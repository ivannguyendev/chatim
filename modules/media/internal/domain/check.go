package domain

import (
	"cmp"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"mime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxNameBytes        = 255
	maxContentTypeBytes = 255
	maxKeyBytes         = 512
	maxETagBytes        = 128
	keyDateLayout       = "20060102"
	keyRandomChars      = 26
	keyAlphabet         = "0123456789abcdefghijklmnopqrstuv"
	keyMillisBytes      = 6
	dimValues           = 2
	hashPrefix          = "sha256:"
	hashHexChars        = 64
)

var keyEncoding = base32.HexEncoding.WithPadding(base32.NoPadding)

type UploadedPart struct {
	Number int32
	ETag   string
}

func ValidateStart(name, contentType string, size int64, l Limits) (string, error) {
	switch {
	case name == "" || len(name) > maxNameBytes || !utf8.ValidString(name) || strings.ContainsFunc(name, hiddenNameRune):
		return "", fmt.Errorf("%w: file name must be 1..%d bytes of printable UTF-8", ErrInvalid, maxNameBytes)
	case len(contentType) > maxContentTypeBytes || !isMediaType(contentType):
		return "", fmt.Errorf("%w: content type must look like type/subtype", ErrInvalid)
	case size < 1 || size > l.MaxFileBytes:
		return "", fmt.Errorf("%w: size %d outside 1..%d", ErrInvalid, size, l.MaxFileBytes)
	}
	return strings.ToLower(contentType), nil
}

func ValidateFileFacts(dim []int32, dur int64) error {
	if len(dim) != 0 && (len(dim) != dimValues || dim[0] < 1 || dim[1] < 1) {
		return fmt.Errorf("%w: dim must be empty or [width, height] of at least 1", ErrInvalid)
	}
	if dur < 0 {
		return fmt.Errorf("%w: dur %d is negative", ErrInvalid, dur)
	}
	return nil
}

func ValidateHash(hash string) error {
	if hash == "" {
		return nil
	}
	hex, ok := strings.CutPrefix(hash, hashPrefix)
	if !ok || len(hex) != hashHexChars || strings.Trim(hex, "0123456789abcdef") != "" {
		return fmt.Errorf("%w: hash must be %s and %d lowercase hex characters", ErrInvalid, hashPrefix, hashHexChars)
	}
	return nil
}

func hiddenNameRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

func isMediaType(v string) bool {
	parsed, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	kind, sub, ok := strings.Cut(parsed, "/")
	return ok && kind != "" && sub != ""
}

func NewObjectKey(tenant, user string, now time.Time, random [10]byte) string {
	var id [keyMillisBytes + len(random)]byte
	var millis [8]byte
	binary.BigEndian.PutUint64(millis[:], uint64(now.UnixMilli()))
	copy(id[:keyMillisBytes], millis[8-keyMillisBytes:])
	copy(id[keyMillisBytes:], random[:])
	return tenant + "/" + user + "/" + now.UTC().Format(keyDateLayout) + "/" + strings.ToLower(keyEncoding.EncodeToString(id[:]))
}

func ThumbnailKey(key, suffix string) string {
	return key + suffix
}

func OwnsKey(key, tenant, user string) bool {
	if tenant == "" || user == "" || len(key) > maxKeyBytes {
		return false
	}
	rest, ok := strings.CutPrefix(key, tenant+"/"+user+"/")
	if !ok {
		return false
	}
	date, random, ok := strings.Cut(rest, "/")
	if !ok || len(random) != keyRandomChars || strings.Trim(random, keyAlphabet) != "" {
		return false
	}
	_, err := time.Parse(keyDateLayout, date)
	return err == nil
}

func CheckPartNumbers(numbers []int32, partCount, maxPerCall int) error {
	if len(numbers) == 0 || len(numbers) > maxPerCall {
		return fmt.Errorf("%w: ask for 1..%d parts", ErrInvalid, maxPerCall)
	}
	seen := make(map[int32]bool, len(numbers))
	for _, n := range numbers {
		if n < 1 || int(n) > partCount || seen[n] {
			return fmt.Errorf("%w: part %d is not a new part in 1..%d", ErrInvalid, n, partCount)
		}
		seen[n] = true
	}
	return nil
}

func CheckUploadedParts(parts []UploadedPart, partCount int) ([]UploadedPart, error) {
	if len(parts) != partCount {
		return nil, fmt.Errorf("%w: %d parts sent, upload has %d", ErrInvalid, len(parts), partCount)
	}
	sorted := slices.SortedFunc(slices.Values(parts), func(a, b UploadedPart) int { return cmp.Compare(a.Number, b.Number) })
	for i, p := range sorted {
		if int(p.Number) != i+1 || p.ETag == "" || len(p.ETag) > maxETagBytes {
			return nil, fmt.Errorf("%w: parts must be 1..%d once each with an ETag", ErrInvalid, partCount)
		}
	}
	return sorted, nil
}
