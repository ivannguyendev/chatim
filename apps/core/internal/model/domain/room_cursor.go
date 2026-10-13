package domain

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"
)

const roomCursorBytes = 26

var roomCursorEncoding = base64.RawURLEncoding.Strict()

func EncodeRoomCursor(folder RoomFolder, k RoomSortKey) string {
	b := make([]byte, 2, roomCursorBytes)
	b[0] = byte(folder)
	if k.Pinned {
		b[1] = 1
	}
	b = binary.BigEndian.AppendUint64(b, cursorMillis(k.PinnedAt))
	b = binary.BigEndian.AppendUint64(b, cursorMillis(k.SortAt))
	b = binary.BigEndian.AppendUint64(b, k.Room)
	return roomCursorEncoding.EncodeToString(b)
}

func DecodeRoomCursor(s string, folder RoomFolder) (RoomSortKey, error) {
	b, err := roomCursorEncoding.DecodeString(s)
	if err != nil || len(b) != roomCursorBytes || RoomFolder(b[0]) != folder || b[1] > 1 {
		return RoomSortKey{}, invalid("next")
	}
	pinnedMs, sortMs := binary.BigEndian.Uint64(b[2:10]), binary.BigEndian.Uint64(b[10:18])
	k := RoomSortKey{Pinned: b[1] == 1, Room: binary.BigEndian.Uint64(b[18:])}
	if k.Room == 0 || pinnedMs > math.MaxInt64 || sortMs > math.MaxInt64 || k.Pinned == (pinnedMs == 0) {
		return RoomSortKey{}, invalid("next")
	}
	k.PinnedAt, k.SortAt = cursorTime(int64(pinnedMs)), cursorTime(int64(sortMs))
	return k, nil
}

func cursorMillis(t time.Time) uint64 {
	ms := toMillis(t).UnixMilli()
	if t.IsZero() || ms <= 0 {
		return 0
	}
	return uint64(ms)
}

func cursorTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
