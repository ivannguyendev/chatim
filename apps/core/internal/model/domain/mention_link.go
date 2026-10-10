package domain

import (
	"strconv"
	"time"
)

const MentionAll MentionKind = 3

type Mention struct {
	Key       MsgKey
	Tenant    string
	Target    MentionTarget
	Sender    string
	Live      bool
	Ver       uint32
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (t MentionTarget) Name(room uint64) string {
	switch t.Kind {
	case MentionUser:
		return "user:" + t.ID
	case MentionGroup:
		return "group:" + t.ID
	case MentionAll:
		return "all:" + strconv.FormatUint(room, 10)
	default:
		return ""
	}
}

func ValidMentionLink(t MentionTarget) bool {
	if t.Kind == MentionAll {
		return t.ID == ""
	}
	return validMentionTarget(t)
}

func MentionTargetsOf(m Message) []MentionTarget {
	if m.Deleted {
		return nil
	}
	out := make([]MentionTarget, 0, len(m.Mentions)+1)
	out = append(out, m.Mentions...)
	if m.MentionAll {
		out = append(out, MentionTarget{Kind: MentionAll})
	}
	return out
}
