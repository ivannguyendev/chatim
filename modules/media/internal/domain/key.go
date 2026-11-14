package domain

import (
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	PublicKeySegment = "pub"
	maxKeyBytes      = 512
	keyDateLayout    = "20060102"
	keyRandomChars   = 26
	keyAlphabet      = "0123456789abcdefghijklmnopqrstuv"
	keyMillisBytes   = 6
	privateKeyParts  = 4
	publicKeyParts   = 5
)

var keyEncoding = base32.HexEncoding.WithPadding(base32.NoPadding)

type KeyInfo struct {
	Tenant string
	User   string
	Public bool
}

func NewObjectKey(tenant, user string, public bool, now time.Time, random [10]byte) string {
	var id [keyMillisBytes + len(random)]byte
	var millis [8]byte
	binary.BigEndian.PutUint64(millis[:], uint64(now.UnixMilli()))
	copy(id[:keyMillisBytes], millis[8-keyMillisBytes:])
	copy(id[keyMillisBytes:], random[:])
	prefix := tenant + "/" + user + "/"
	if public {
		prefix = PublicKeyPrefix(tenant, user)
	}
	return prefix + now.UTC().Format(keyDateLayout) + "/" + strings.ToLower(keyEncoding.EncodeToString(id[:]))
}

func PublicKeyPrefix(tenant, user string) string {
	return tenant + "/" + user + "/" + PublicKeySegment + "/"
}

func ThumbnailKey(key, suffix string) string {
	return key + suffix
}

func ParseKey(key string) (KeyInfo, bool) {
	if len(key) > maxKeyBytes {
		return KeyInfo{}, false
	}
	parts := strings.Split(key, "/")
	info := KeyInfo{Public: len(parts) == publicKeyParts && parts[2] == PublicKeySegment}
	if !info.Public && len(parts) != privateKeyParts {
		return KeyInfo{}, false
	}
	info.Tenant, info.User = parts[0], parts[1]
	date, random := parts[len(parts)-2], parts[len(parts)-1]
	if info.Tenant == "" || info.User == "" || len(random) != keyRandomChars || strings.Trim(random, keyAlphabet) != "" {
		return KeyInfo{}, false
	}
	if _, err := time.Parse(keyDateLayout, date); err != nil {
		return KeyInfo{}, false
	}
	return info, true
}

func OwnsKey(key, tenant, user string) bool {
	info, ok := ParseKey(key)
	return ok && info.Tenant == tenant && info.User == user
}

func ValidatePublicType(contentType string) error {
	if !strings.HasPrefix(contentType, thumbnailImagePrefix) {
		return fmt.Errorf("%w: public files must be images", ErrInvalid)
	}
	return nil
}
