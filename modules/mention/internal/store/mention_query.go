package store

import (
	"bytes"
	"cmp"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type MentionCursor struct {
	At  time.Time
	Key domain.MessageKey
}

type TargetMentionQuery struct {
	Tenant string
	Target string
	Before MentionCursor
	Limit  int
}

type SharedRoom struct {
	Room  uint64
	Since time.Time
}

type SharedMentionQuery struct {
	Tenant     string
	Rooms      []SharedRoom
	Groups     []string
	GroupSince time.Time
	Before     MentionCursor
	Limit      int
}

func ValidateTargetMentionQuery(q TargetMentionQuery) error {
	if err := ids.ValidTenant(q.Tenant); err != nil {
		return err
	}
	if q.Target == "" {
		return invalid("mention target")
	}
	return validateMentionPage(q.Before, q.Limit)
}

func ValidateSharedMentionQuery(q SharedMentionQuery) error {
	if err := ids.ValidTenant(q.Tenant); err != nil {
		return err
	}
	if len(q.Rooms) == 0 || len(q.Rooms) > SharedMentionBatch {
		return invalid("mention rooms")
	}
	seen := make(map[uint64]struct{}, len(q.Rooms))
	for _, r := range q.Rooms {
		if _, dup := seen[r.Room]; dup || ValidateRoom(r.Room) != nil {
			return invalid("mention room")
		}
		seen[r.Room] = struct{}{}
	}
	for _, g := range q.Groups {
		if !keys.ValidMentionID(g) {
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

func (q TargetMentionQuery) Wants(m domain.Mention) bool {
	return m.State == domain.StateLive && m.Tenant == q.Tenant && m.Target.Name(m.Key.Room) == q.Target && q.Before.Holds(m)
}

func (q SharedMentionQuery) Wants(m domain.Mention) bool {
	if m.State != domain.StateLive || m.Tenant != q.Tenant || !q.Before.Holds(m) {
		return false
	}
	switch m.Target.Kind {
	case domain.TargetAll:
	case domain.TargetGroup:
		if !slices.Contains(q.Groups, m.Target.ID) || m.CreatedAt.Before(q.GroupSince) {
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
	return c.At.IsZero() || CompareMentions(m, domain.Mention{Key: c.Key, CreatedAt: c.At}) < 0
}

func CursorOf(m domain.Mention) MentionCursor {
	return MentionCursor{At: m.CreatedAt, Key: m.Key}
}

func CompareMentions(a, b domain.Mention) int {
	return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), bytes.Compare(mentionKeyBytes(a), mentionKeyBytes(b)))
}

func mentionKeyBytes(m domain.Mention) []byte {
	return keys.Msg(m.Key.Room, m.Key.Thread, m.Key.Seq)
}
