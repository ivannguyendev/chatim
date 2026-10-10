package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type MentionSet struct {
	Key       MsgKey
	Tenant    string
	Sender    string
	Ver       uint32
	Targets   []domain.MentionTarget
	CreatedAt time.Time
	At        time.Time
}

type MentionPlan struct {
	Live   []domain.MentionTarget
	Retire []domain.MentionTarget
}

type Mentions interface {
	ApplyMentions(ctx context.Context, s MentionSet) error
	MentionsOf(ctx context.Context, key MsgKey) ([]domain.Mention, error)
	List(ctx context.Context, q MentionQuery) ([]domain.Mention, error)
}

func ValidateMentionSet(s MentionSet) error {
	switch {
	case s.Key.Validate() != nil:
		return invalid("mention message")
	case s.Tenant == "":
		return invalid("tenant")
	case domain.ValidUser(s.Sender) != nil:
		return invalid("mention sender")
	case s.CreatedAt.IsZero() || s.At.IsZero():
		return invalid("time")
	}
	seen := make(map[domain.MentionTarget]struct{}, len(s.Targets))
	for _, t := range s.Targets {
		if _, dup := seen[t]; dup || !domain.ValidMentionLink(t) {
			return invalid("mention target")
		}
		seen[t] = struct{}{}
	}
	return nil
}

func PlanMentions(stored []domain.Mention, s MentionSet) MentionPlan {
	have := make(map[domain.MentionTarget]domain.Mention, len(stored))
	for _, d := range stored {
		if d.Ver > s.Ver {
			return MentionPlan{}
		}
		have[d.Target] = d
	}
	var p MentionPlan
	want := make(map[domain.MentionTarget]struct{}, len(s.Targets))
	for _, t := range s.Targets {
		want[t] = struct{}{}
		if cur, ok := have[t]; !ok || !cur.Live || cur.Ver < s.Ver {
			p.Live = append(p.Live, t)
		}
	}
	for _, d := range stored {
		if _, keep := want[d.Target]; !keep && (d.Live || d.Ver < s.Ver) {
			p.Retire = append(p.Retire, d.Target)
		}
	}
	return p
}
