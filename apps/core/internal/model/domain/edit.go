package domain

import "time"

type EditKind uint8

const (
	EditText EditKind = iota + 1
	EditDelete
	EditOriginal
)

type Edit struct {
	Room    uint64
	Thread  uint64
	Seq     uint64
	Version uint32
	Kind    EditKind
	Tenant  string
	By      string
	Text    string
	At      time.Time

	Mentions     []MentionTarget
	MentionAll   bool
	MentionAllAt time.Time
}

func (e Edit) MentionSet() Mentions {
	return Mentions{Targets: e.Mentions, All: e.MentionAll, AllAt: e.MentionAllAt}
}
