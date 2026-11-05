package domain

import (
	"slices"
	"time"
)

type Mentions struct {
	Targets []MentionTarget
	All     bool
	AllAt   time.Time
}

func (n Mentions) Stamp(prev Mentions, at time.Time) Mentions {
	out := Mentions{All: n.All}
	if len(n.Targets) > 0 {
		out.Targets = make([]MentionTarget, len(n.Targets))
	}
	for i, t := range n.Targets {
		t.At = at
		if j := slices.IndexFunc(prev.Targets, t.sameAs); j >= 0 {
			t.At = prev.Targets[j].At
		}
		out.Targets[i] = t
	}
	switch {
	case n.All && prev.All:
		out.AllAt = prev.AllAt
	case n.All:
		out.AllAt = at
	}
	return out
}

func (t MentionTarget) sameAs(o MentionTarget) bool {
	return t.Identity() == o.Identity()
}
