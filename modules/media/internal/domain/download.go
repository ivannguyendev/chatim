package domain

import "strings"

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

type File struct{ Key, Name, Mime string }

type MessageFiles struct {
	Ref   MessageRef
	Files []File
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
