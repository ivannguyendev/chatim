package store

import (
	"cmp"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
)

type MentionRoom struct {
	Room    uint64
	Since   time.Time
	Last    time.Time
	ReadSeq uint64
}

func MentionSince(r domain.MemberRoom) time.Time {
	if r.ClearedAt.After(r.JoinedAt) {
		return r.ClearedAt
	}
	return r.JoinedAt
}

func SharedMentionRooms(rooms []domain.MemberRoom, last map[uint64]time.Time) []MentionRoom {
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

func RaiseSinceToRead(rooms []MentionRoom, readAt map[uint64]time.Time) []MentionRoom {
	out := make([]MentionRoom, 0, len(rooms))
	for _, r := range rooms {
		if at, ok := readAt[r.Room]; ok && at.After(r.Since) {
			r.Since = at
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
	seen := make(map[domain.MessageKey]struct{}, len(all))
	all = slices.DeleteFunc(all, func(m domain.Mention) bool {
		_, dup := seen[m.Key]
		seen[m.Key] = struct{}{}
		return dup
	})
	return all[:min(len(all), limit)]
}
