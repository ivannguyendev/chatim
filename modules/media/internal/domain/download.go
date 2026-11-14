package domain

import (
	"strconv"
	"strings"
	"time"
)

const (
	thumbnailImagePrefix = "image/"
	thumbnailVideoPrefix = "video/"
)

type Caller struct{ Tenant, User string }

type MessageRef struct {
	Room   string
	Thread uint64
	Seq    uint64
}

func (r MessageRef) Key() string {
	return r.Room + "-" + strconv.FormatUint(r.Thread, 10) + "-" + strconv.FormatUint(r.Seq, 10)
}

type File struct {
	Key  string
	Name string
	Mime string
	Size int64
	Dim  []int32
	Dur  int64
	Hash string
}

type MessageFiles struct {
	Ref    MessageRef
	Sender string
	SentAt time.Time
	Files  []File
}

type Thumbnails struct {
	Enabled bool
	Suffix  string
}

func (t Thumbnails) KeyFor(key, mime string) (string, bool) {
	if !t.Enabled || (!strings.HasPrefix(mime, thumbnailImagePrefix) && !strings.HasPrefix(mime, thumbnailVideoPrefix)) {
		return "", false
	}
	return ThumbnailKey(key, t.Suffix), true
}
