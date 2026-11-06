package domain

import (
	"cmp"
	"encoding/base32"
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
)

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type UploadedPart struct {
	Number int32
	ETag   string
}

func ValidateStart(name, contentType string, size int64, l Limits) error {
	switch {
	case name == "" || len(name) > maxNameBytes || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl):
		return fmt.Errorf("%w: file name must be 1..%d bytes of printable UTF-8", ErrInvalid, maxNameBytes)
	case len(contentType) > maxContentTypeBytes || !isMediaType(contentType):
		return fmt.Errorf("%w: content type must look like type/subtype", ErrInvalid)
	case size < 1 || size > l.MaxFileBytes:
		return fmt.Errorf("%w: size %d outside 1..%d", ErrInvalid, size, l.MaxFileBytes)
	}
	return nil
}

func isMediaType(v string) bool {
	parsed, _, err := mime.ParseMediaType(v)
	if err != nil {
		return false
	}
	kind, sub, ok := strings.Cut(parsed, "/")
	return ok && kind != "" && sub != ""
}

func NewObjectKey(tenant, user string, now time.Time, random [16]byte) string {
	return tenant + "/" + user + "/" + now.UTC().Format(keyDateLayout) + "/" + strings.ToLower(keyEncoding.EncodeToString(random[:]))
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
	if !ok || len(random) != keyRandomChars || strings.Contains(random, "/") {
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
