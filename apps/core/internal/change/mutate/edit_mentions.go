package mutate

import (
	"context"

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
	c.mentions, c.all = targets, set.All
	return c, nil
}

func (m *Mutator) resolveMentions(ctx context.Context, c change, grant access.Request, msg domain.Message) (change, error) {
	switch {
	case c.kind != domain.EditText:
		return c, nil
	case c.keepMentions:
		c.mentions, c.all = msg.Mentions, msg.MentionAll
		return c, nil
	case !c.all:
		return c, nil
	}
	grant.Action = access.MentionAll
	if err := m.d.Access.Allow(ctx, grant); err != nil {
		return change{}, err
	}
	return c, nil
}

func (c change) matches(e domain.Edit) bool {
	if e.By != c.user || e.Kind != c.kind || e.Text != c.text {
		return false
	}
	return c.keepMentions || (e.MentionAll == c.all && domain.SameMentions(e.Mentions, c.mentions))
}
