package mutate

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type MentionSet struct {
	Targets []domain.MentionTarget
	All     bool
}

func (m *Mutator) withMentions(c change, set *MentionSet) (change, error) {
	if set == nil {
		c.keepMentions = true
		return c, nil
	}
	targets, err := domain.ValidateMentions(set.Targets, m.d.Limits.MentionTargets)
	if err != nil {
		return change{}, err
	}
	c.mentions = domain.Mentions{Targets: targets, All: set.All}
	return c, nil
}

func (m *Mutator) resolveMentions(ctx context.Context, c change, grant access.Request) error {
	if c.kind != domain.EditText || c.keepMentions || !c.mentions.All {
		return nil
	}
	grant.Action = access.MentionAll
	return m.d.Access.Allow(ctx, grant)
}

func previousMentions(msg domain.Message, latest domain.Edit, found bool) domain.Mentions {
	if found && latest.Version > msg.Version {
		return latest.MentionSet()
	}
	return msg.MentionSet()
}

func (c change) stamped(prev domain.Mentions, at time.Time) change {
	if c.kind == domain.EditDelete || c.keepMentions {
		c.mentions = prev
		return c
	}
	c.mentions = c.mentions.Stamp(prev, at)
	return c
}

func (c change) matches(e domain.Edit) bool {
	if e.By != c.user || e.Kind != c.kind || e.Text != c.text {
		return false
	}
	if c.keepMentions || c.kind == domain.EditDelete {
		return true
	}
	return e.MentionAll == c.mentions.All && domain.SameMentions(e.Mentions, c.mentions.Targets)
}
