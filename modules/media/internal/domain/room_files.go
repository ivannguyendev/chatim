package domain

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type RoomFileKind string

const (
	RoomFileMedia RoomFileKind = "media"
	RoomFileOther RoomFileKind = "file"
)

func RoomFileKindOf(mime string) RoomFileKind {
	if strings.HasPrefix(mime, thumbnailImagePrefix) || strings.HasPrefix(mime, thumbnailVideoPrefix) {
		return RoomFileMedia
	}
	return RoomFileOther
}

func (k RoomFileKind) Valid() bool { return k == RoomFileMedia || k == RoomFileOther }

type RoomFileCursor struct {
	SentAt     time.Time
	MessageKey string
	FileKey    string
}

func CursorOf(p MessageFile) RoomFileCursor {
	return RoomFileCursor{SentAt: p.SentAt.Truncate(time.Millisecond).UTC(), MessageKey: p.MessageKey, FileKey: p.FileKey}
}

func (c RoomFileCursor) IsZero() bool { return c.MessageKey == "" }

func (c RoomFileCursor) Encode() string {
	if c.IsZero() {
		return ""
	}
	raw := strconv.FormatInt(c.SentAt.UnixMilli(), 10) + "|" + c.MessageKey + "|" + c.FileKey
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func ParseRoomFileCursor(s string) (RoomFileCursor, error) {
	if s == "" {
		return RoomFileCursor{}, nil
	}
	bad := fmt.Errorf("%w: before is not a room file cursor", ErrInvalid)
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return RoomFileCursor{}, bad
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return RoomFileCursor{}, bad
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || ms < 0 {
		return RoomFileCursor{}, bad
	}
	return RoomFileCursor{SentAt: time.UnixMilli(ms).UTC(), MessageKey: parts[1], FileKey: parts[2]}, nil
}

type RoomAccess struct {
	Room      string
	ClearedAt time.Time
}

type RoomFilesQuery struct {
	Tenant    string
	Room      string
	Kind      RoomFileKind
	ClearedAt time.Time
	Before    RoomFileCursor
	Limit     int
}

func ParseMessageKey(key string) (MessageRef, error) {
	parts := strings.Split(key, "-")
	bad := fmt.Errorf("%w: message key %q", ErrInvalid, key)
	if len(parts) != 3 {
		return MessageRef{}, bad
	}
	if _, err := strconv.ParseUint(parts[0], 10, 64); err != nil {
		return MessageRef{}, bad
	}
	thread, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return MessageRef{}, bad
	}
	seq, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || seq == 0 {
		return MessageRef{}, bad
	}
	return MessageRef{Room: parts[0], Thread: thread, Seq: seq}, nil
}
