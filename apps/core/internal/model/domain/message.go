package domain

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Kind uint8

const (
	KindText   Kind = 1
	KindSystem Kind = 2
)

var kindNames = map[string]Kind{
	"text":   KindText,
	"system": KindSystem,
}

func ParseKind(name string) (Kind, error) {
	if k, ok := kindNames[name]; ok {
		return k, nil
	}
	return 0, fmt.Errorf("%w: unknown message kind %q", apperr.ErrInvalidArgument, name)
}

type Message struct {
	Room         uint64
	Thread       uint64
	Seq          uint64
	Tenant       string
	From         string
	Kind         Kind
	Text         string
	CID          string
	CreatedAt    time.Time
	Version      uint32
	Deleted      bool
	EditedAt     time.Time
	Hidden       bool
	Reactions    ReactionSummary
	ReplyTo      *ReplyRef
	Forward      *ForwardRef
	Mentions     []MentionTarget
	MentionAll   bool
	MentionAllAt time.Time
	Replies      ReplyCount
	SkipUnread   bool
	Meta         []byte
}

func (m Message) WithoutDeletedContent() Message {
	if !m.Deleted {
		return m
	}
	m.Text = ""
	m.Mentions, m.MentionAll, m.MentionAllAt, m.Forward, m.Meta = nil, false, time.Time{}, nil, nil
	m.Reactions, m.Replies = ReactionSummary{}, ReplyCount{}
	return m
}

func ValidateSystemMessage(m Message) error {
	switch {
	case m.Kind != KindSystem:
		return nil
	case m.ReplyTo != nil:
		return invalid("reply_to")
	case m.Forward != nil:
		return invalid("forward_from")
	case len(m.Mentions) > 0 || m.MentionAll:
		return invalid("mentions")
	default:
		return nil
	}
}

func (m Message) MentionSet() Mentions {
	return Mentions{Targets: m.Mentions, All: m.MentionAll, AllAt: m.MentionAllAt}
}

func (m Message) WithMentions(n Mentions) Message {
	m.Mentions, m.MentionAll, m.MentionAllAt = n.Targets, n.All, n.AllAt
	return m
}
