package domain

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Kind uint8

const KindText Kind = 1

var kindNames = map[string]Kind{
	"text": KindText,
}

func ParseKind(name string) (Kind, error) {
	if k, ok := kindNames[name]; ok {
		return k, nil
	}
	return 0, fmt.Errorf("%w: unknown message kind %q", apperr.ErrInvalidArgument, name)
}

type Message struct {
	Room       uint64
	Thread     uint64
	Seq        uint64
	Tenant     string
	From       string
	Kind       Kind
	Text       string
	CID        string
	CreatedAt  time.Time
	Version    uint32
	Deleted    bool
	EditedAt   time.Time
	Hidden     bool
	Reactions  ReactionSummary
	ReplyTo    *ReplyRef
	Forward    *ForwardRef
	Mentions   []MentionTarget
	MentionAll bool
	Replies    ReplyCount
}

func (m Message) WithoutDeletedContent() Message {
	if !m.Deleted {
		return m
	}
	m.Text = ""
	m.Mentions, m.MentionAll, m.Forward = nil, false, nil
	m.Reactions, m.Replies = ReactionSummary{}, ReplyCount{}
	return m
}
