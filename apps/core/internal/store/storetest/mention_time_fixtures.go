package storetest

import (
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func stampedAt(t domain.MentionTarget, at time.Time) domain.MentionTarget {
	t.At = at
	return t
}

func sameTarget(a, b domain.MentionTarget) bool {
	return a.Identity() == b.Identity() && a.At.Equal(b.At)
}

func sameMentionSet(a, b domain.Mentions) bool {
	return a.All == b.All && a.AllAt.Equal(b.AllAt) && (a.Targets == nil) == (b.Targets == nil) && slices.EqualFunc(a.Targets, b.Targets, sameTarget)
}
