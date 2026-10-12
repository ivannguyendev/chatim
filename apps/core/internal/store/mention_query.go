package store

import (
	"bytes"
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const SharedMentionBatch = 200

type MentionCursor struct {
	At  time.Time
	Key MsgKey
}

type TargetMentionQuery struct {
	Tenant     string
	Target     string
	UnreadOnly bool
	Before     MentionCursor
	Limit      int
}

type SharedRoom struct {
	Room  uint64
	Since time.Time
}

type SharedMentionQuery struct {
	Rooms  []SharedRoom
	Groups []string
	Before MentionCursor
	Limit  int
}

func ValidateTargetMentionQuery(q TargetMentionQuery) error {
	switch {
	case q.Tenant == "":
		return invalid("tenant")
	case q.Target == "":
		return invalid("mention target")
	}
	return validateMentionPage(q.Before, q.Limit)
}

func ValidateSharedMentionQuery(q SharedMentionQuery) error {
	if len(q.Rooms) == 0 || len(q.Rooms) > SharedMentionBatch {
		return invalid("mention rooms")
	}
	seen := make(map[uint64]struct{}, len(q.Rooms))
	for _, r := range q.Rooms {
		if _, dup := seen[r.Room]; dup || r.Room == 0 || r.Room > math.MaxInt64 {
			return invalid("mention room")
		}
		seen[r.Room] = struct{}{}
	}
	for _, g := range q.Groups {
		if !domain.ValidMentionLink(domain.MentionTarget{Kind: domain.MentionGroup, ID: g}) {
			return invalid("mention group")
		}
	}
	return validateMentionPage(q.Before, q.Limit)
}

func validateMentionPage(before MentionCursor, limit int) error {
	if !before.At.IsZero() && before.Key.Validate() != nil {
		return invalid("mention cursor")
	}
	return ValidateLimit(limit, MaxPageLimit)
}

func (q SharedMentionQuery) Wants(m domain.Mention) bool {
	if !m.Live || !q.Before.Holds(m) {
		return false
	}
	switch m.Target.Kind {
	case domain.MentionAll:
	case domain.MentionGroup:
		if !slices.Contains(q.Groups, m.Target.ID) {
			return false
		}
	default:
		return false
	}
	for _, r := range q.Rooms {
		if r.Room == m.Key.Room {
			return m.CreatedAt.After(r.Since)
		}
	}
	return false
}

func (c MentionCursor) Holds(m domain.Mention) bool {
	return c.At.IsZero() || CompareMentions(m, domain.Mention{Key: domain.MsgKey(c.Key), CreatedAt: c.At}) < 0
}

func CursorOf(m domain.Mention) MentionCursor {
	return MentionCursor{At: m.CreatedAt, Key: MsgKey(m.Key)}
}

func CompareMentions(a, b domain.Mention) int {
	return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), bytes.Compare(mentionKeyBytes(a), mentionKeyBytes(b)))
}

func mentionKeyBytes(m domain.Mention) []byte {
	return keys.Msg(m.Key.Room, m.Key.Thread, m.Key.Seq)
}
