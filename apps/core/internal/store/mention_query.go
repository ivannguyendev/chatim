package store

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type MentionWant struct {
	Target string
	Since  time.Time
}

type MentionCursor struct {
	At  time.Time
	Key MsgKey
}

type MentionQuery struct {
	Tenant  string
	Targets []MentionWant
	Before  MentionCursor
	Limit   int
}

func ValidateMentionQuery(q MentionQuery) error {
	if q.Tenant == "" {
		return invalid("tenant")
	}
	if len(q.Targets) == 0 {
		return invalid("mention targets")
	}
	seen := make(map[string]struct{}, len(q.Targets))
	for _, w := range q.Targets {
		if _, dup := seen[w.Target]; dup || w.Target == "" {
			return invalid("mention target")
		}
		seen[w.Target] = struct{}{}
	}
	if !q.Before.At.IsZero() && q.Before.Key.Validate() != nil {
		return invalid("mention cursor")
	}
	return ValidateLimit(q.Limit, MaxPageLimit)
}

func (q MentionQuery) Wants(m domain.Mention) bool {
	name := m.Target.Name(m.Key.Room)
	for _, w := range q.Targets {
		if w.Target == name {
			return w.Since.IsZero() || !m.CreatedAt.Before(w.Since)
		}
	}
	return false
}
