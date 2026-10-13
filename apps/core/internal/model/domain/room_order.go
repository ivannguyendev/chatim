package domain

import (
	"cmp"
	"time"
)

type RoomFolder uint8

const (
	FolderMain     RoomFolder = 1
	FolderArchived RoomFolder = 2
)

type RoomSortKey struct {
	Pinned   bool
	PinnedAt time.Time
	SortAt   time.Time
	Room     uint64
}

type RoomItem struct {
	Room        Room
	Me          Member
	Preview     RoomPreview
	Unread      Unread
	PeerReadSeq uint64
}

func FolderOf(p RoomPreferences) RoomFolder {
	if p.ArchivedAt.IsZero() {
		return FolderMain
	}
	return FolderArchived
}

func NewRoomSortKey(room uint64, pinnedAt, lastMessageAt, joinedAt time.Time) RoomSortKey {
	sortAt := joinedAt
	if lastMessageAt.After(sortAt) {
		sortAt = lastMessageAt
	}
	k := RoomSortKey{Room: room, SortAt: toMillis(sortAt)}
	if !pinnedAt.IsZero() {
		k.Pinned, k.PinnedAt = true, toMillis(pinnedAt)
	}
	return k
}

func CompareRoomSortKeys(a, b RoomSortKey) int {
	if a.Pinned != b.Pinned {
		if a.Pinned {
			return -1
		}
		return 1
	}
	if c := b.PinnedAt.Compare(a.PinnedAt); c != 0 {
		return c
	}
	if c := b.SortAt.Compare(a.SortAt); c != 0 {
		return c
	}
	return cmp.Compare(b.Room, a.Room)
}

func toMillis(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Millisecond)
}
