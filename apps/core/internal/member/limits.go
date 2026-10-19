package member

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultMemberBatch = 500
	MinMemberBatch     = 2
)

type Limits struct {
	MemberBatch int
}

func (l Limits) Validate() error { return l.withDefaults().validate() }

func (l Limits) withDefaults() Limits {
	l.MemberBatch = cmp.Or(l.MemberBatch, DefaultMemberBatch)
	return l
}

func (l Limits) validate() error {
	if l.MemberBatch < MinMemberBatch || l.MemberBatch > domain.MaxMemberBatch {
		return fmt.Errorf("%w: member batch %d must be %d to %d", apperr.ErrInvalidArgument, l.MemberBatch, MinMemberBatch, domain.MaxMemberBatch)
	}
	return nil
}
