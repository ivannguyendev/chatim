package mutate

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Limits struct {
	MentionTargets int
}

func (l Limits) Validate() error { return l.withDefaults().validate() }

func (l Limits) withDefaults() Limits {
	l.MentionTargets = cmp.Or(l.MentionTargets, domain.DefaultMentionTargetsMax)
	return l
}

func (l Limits) validate() error {
	if l.MentionTargets < 1 {
		return fmt.Errorf("%w: mention targets %d must be at least 1", apperr.ErrInvalidArgument, l.MentionTargets)
	}
	return nil
}
