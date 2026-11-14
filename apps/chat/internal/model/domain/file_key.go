package domain

import (
	"encoding/base32"
	"strings"
	"time"
)

const (
	maxFileKeyBytes      = 512
	fileKeyDate          = "20060102"
	fileKeyRandom        = 26
	fileKeyRawBytes      = 16
	fileKeyMillisBytes   = 6
	publicFileKeySegment = "pub"
	privateFileKeyParts  = 4
	publicFileKeyParts   = 5
	fileKeyMaxAge        = 24 * time.Hour
	fileKeyMaxSkew       = 5 * time.Minute
)

var fileKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

type fileKey struct {
	tenant  string
	user    string
	date    string
	created time.Time
	public  bool
}

func parseFileKey(key string) (fileKey, bool) {
	if len(key) > maxFileKeyBytes {
		return fileKey{}, false
	}
	parts := strings.Split(key, "/")
	public := len(parts) == publicFileKeyParts && parts[2] == publicFileKeySegment
	if !public && len(parts) != privateFileKeyParts {
		return fileKey{}, false
	}
	date, random := parts[len(parts)-2], parts[len(parts)-1]
	if len(random) != fileKeyRandom {
		return fileKey{}, false
	}
	if _, err := time.Parse(fileKeyDate, date); err != nil {
		return fileKey{}, false
	}
	raw, err := fileKeyCode.DecodeString(random)
	if err != nil || len(raw) != fileKeyRawBytes {
		return fileKey{}, false
	}
	var ms int64
	for _, b := range raw[:fileKeyMillisBytes] {
		ms = ms<<8 | int64(b)
	}
	return fileKey{tenant: parts[0], user: parts[1], date: date, created: time.UnixMilli(ms).UTC(), public: public}, true
}

func (k fileKey) freshAt(now time.Time) bool {
	return k.created.Format(fileKeyDate) == k.date &&
		!k.created.Before(now.Add(-fileKeyMaxAge)) && !k.created.After(now.Add(fileKeyMaxSkew))
}
