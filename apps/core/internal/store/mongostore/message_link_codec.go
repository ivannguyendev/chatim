package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	mentionUserName  = "user"
	mentionGroupName = "group"
)

type replyDoc struct {
	Thread int64 `bson:"th"`
	Seq    int64 `bson:"s"`
}

type replyCountDoc struct {
	N       int64 `bson:"n"`
	Version int64 `bson:"v"`
}

type forwardDoc struct {
	Room   int64     `bson:"r"`
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	Author string    `bson:"f"`
	SentAt time.Time `bson:"ts"`
}

type mentionDoc struct {
	Kind string `bson:"k"`
	ID   string `bson:"i"`
}

func encodeLinks(m domain.Message, d *messageDoc) error {
	if r := m.ReplyTo; r != nil {
		th, s, err := keyFields("reply", r.Thread, r.Seq)
		if err != nil {
			return err
		}
		d.ReplyTo = &replyDoc{Thread: th, Seq: s}
	}
	if f := m.Forward; f != nil {
		room, err := toInt64("forward room", f.Room)
		if err != nil {
			return err
		}
		th, s, err := keyFields("forward", f.Thread, f.Seq)
		if err != nil {
			return err
		}
		d.Forward = &forwardDoc{Room: room, Thread: th, Seq: s, Author: f.Author, SentAt: f.SentAt}
	}
	for _, t := range m.Mentions {
		name, err := mentionKindName(t.Kind)
		if err != nil {
			return err
		}
		d.Mentions = append(d.Mentions, mentionDoc{Kind: name, ID: t.ID})
	}
	d.MentionAll = m.MentionAll
	return nil
}

func decodeLinks(d messageDoc, m *domain.Message) error {
	if r := d.ReplyTo; r != nil {
		th, s, err := keyValues("reply", r.Thread, r.Seq)
		if err != nil {
			return err
		}
		m.ReplyTo = &domain.ReplyRef{Thread: th, Seq: s}
	}
	if f := d.Forward; f != nil {
		room, err := toUint64("forward room", f.Room)
		if err != nil {
			return err
		}
		th, s, err := keyValues("forward", f.Thread, f.Seq)
		if err != nil {
			return err
		}
		m.Forward = &domain.ForwardRef{Room: room, Thread: th, Seq: s, Author: f.Author, SentAt: f.SentAt}
	}
	for _, t := range d.Mentions {
		if kind, ok := mentionKindOf(t.Kind); ok {
			m.Mentions = append(m.Mentions, domain.MentionTarget{Kind: kind, ID: t.ID})
		}
	}
	if c := d.Replies; c != nil {
		rc, err := decodeReplyCount(*c)
		if err != nil {
			return err
		}
		m.Replies = rc
	}
	m.MentionAll = d.MentionAll
	return nil
}

func decodeReplyCount(c replyCountDoc) (domain.ReplyCount, error) {
	if err := withinUint32("reply count", c.N); err != nil {
		return domain.ReplyCount{}, err
	}
	v, err := toUint64("reply count version", c.Version)
	if err != nil {
		return domain.ReplyCount{}, err
	}
	return domain.SettleReplies(c.N, v), nil
}

func keyFields(link string, thread, seq uint64) (int64, int64, error) {
	th, err := toInt64(link+" thread", thread)
	if err != nil {
		return 0, 0, err
	}
	s, err := toInt64(link+" seq", seq)
	return th, s, err
}

func keyValues(link string, thread, seq int64) (uint64, uint64, error) {
	th, err := toUint64(link+" thread", thread)
	if err != nil {
		return 0, 0, err
	}
	s, err := toUint64(link+" seq", seq)
	return th, s, err
}

func mentionKindName(k domain.MentionKind) (string, error) {
	switch k {
	case domain.MentionUser:
		return mentionUserName, nil
	case domain.MentionGroup:
		return mentionGroupName, nil
	default:
		return "", fmt.Errorf("%w: mention kind %d", apperr.ErrInvalidArgument, k)
	}
}

func mentionKindOf(name string) (domain.MentionKind, bool) {
	switch name {
	case mentionUserName:
		return domain.MentionUser, true
	case mentionGroupName:
		return domain.MentionGroup, true
	default:
		return 0, false
	}
}
