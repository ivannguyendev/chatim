package store

import (
	"cmp"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type MentionRoom struct {
	Room    uint64
	Since   time.Time
	Last    time.Time
	ReadSeq uint64
}

type MentionPage struct {
	Items []domain.Mention
	Next  MentionCursor
}

func MentionSince(r MemberRoom) time.Time {
	if r.ClearedAt.After(r.JoinedAt) {
		return r.ClearedAt
	}
	return r.JoinedAt
}

func SharedMentionRooms(rooms []MemberRoom, last map[uint64]time.Time) []MentionRoom {
	out := make([]MentionRoom, 0, len(last))
	for _, r := range rooms {
		at, ok := last[r.Room]
		if since := MentionSince(r); ok && at.After(since) {
			out = append(out, MentionRoom{Room: r.Room, Since: since, Last: at, ReadSeq: r.ReadSeq})
		}
	}
	slices.SortFunc(out, func(a, b MentionRoom) int { return cmp.Or(b.Last.Compare(a.Last), cmp.Compare(a.Room, b.Room)) })
	return out
}

func ReadMessageKeys(rooms []MentionRoom) []MsgKey {
	out := make([]MsgKey, 0, len(rooms))
	for _, r := range rooms {
		if r.ReadSeq > 0 {
			out = append(out, MsgKey{Room: r.Room, Seq: r.ReadSeq})
		}
	}
	return out
}

func RaiseSinceToRead(rooms []MentionRoom, readAt map[MsgKey]time.Time) []MentionRoom {
	out := make([]MentionRoom, 0, len(rooms))
	for _, r := range rooms {
		if at, ok := readAt[MsgKey{Room: r.Room, Seq: r.ReadSeq}]; ok && r.ReadSeq > 0 {
			if before := at.Add(-time.Millisecond); before.After(r.Since) {
				r.Since = before
			}
		}
		if r.Last.After(r.Since) {
			out = append(out, r)
		}
	}
	return out
}

func MergeMentions(limit int, lists ...[]domain.Mention) []domain.Mention {
	all := slices.Concat(lists...)
	slices.SortStableFunc(all, func(a, b domain.Mention) int { return CompareMentions(b, a) })
	all = slices.CompactFunc(all, func(a, b domain.Mention) bool { return a.Key == b.Key })
	return all[:min(len(all), limit)]
}

func MentionsSettled(merged []domain.Mention, limit int, next time.Time) bool {
	return len(merged) >= limit && merged[limit-1].CreatedAt.After(next)
}

func PageMentions(limit int, user []domain.Mention, userEnd MentionCursor, shared []domain.Mention) MentionPage {
	items := MergeMentions(limit, user, shared)
	if !userEnd.At.IsZero() {
		items = slices.DeleteFunc(items, userEnd.Holds)
	}
	switch {
	case len(items) == limit:
		return MentionPage{Items: items, Next: CursorOf(items[len(items)-1])}
	case !userEnd.At.IsZero():
		return MentionPage{Items: items, Next: userEnd}
	default:
		return MentionPage{Items: items}
	}
}
