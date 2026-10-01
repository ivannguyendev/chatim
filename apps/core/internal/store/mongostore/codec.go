package mongostore

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var errCorrupt = errors.New("mongostore: corrupt document")

type messageDoc struct {
	ID        []byte      `bson:"_id"`
	Tenant    string      `bson:"t"`
	From      string      `bson:"f"`
	Pts       int64       `bson:"p"`
	Kind      domain.Kind `bson:"k"`
	Text      string      `bson:"x"`
	CID       string      `bson:"c"`
	CreatedAt time.Time   `bson:"ts"`
}

type roomDoc struct {
	ID          int64           `bson:"_id"`
	Tenant      string          `bson:"t"`
	Type        domain.RoomType `bson:"ty"`
	Name        string          `bson:"n"`
	CreatedBy   string          `bson:"cb"`
	CreatedAt   time.Time       `bson:"ca"`
	MemberCount int             `bson:"mc"`
}

type memberDoc struct {
	Room     int64       `bson:"r"`
	User     string      `bson:"u"`
	Tenant   string      `bson:"t"`
	Role     domain.Role `bson:"ro"`
	JoinedAt time.Time   `bson:"ja"`
}

func encodeMessage(m domain.Message) (messageDoc, error) {
	if err := store.KeyOf(m).Validate(); err != nil {
		return messageDoc{}, err
	}
	if _, err := toInt64("seq", m.Seq); err != nil {
		return messageDoc{}, err
	}
	pts, err := toInt64("pts", m.Pts)
	if err != nil {
		return messageDoc{}, err
	}
	return messageDoc{
		ID:        keys.Msg(m.Room, m.Thread, m.Seq),
		Tenant:    m.Tenant,
		From:      m.From,
		Pts:       pts,
		Kind:      m.Kind,
		Text:      m.Text,
		CID:       m.CID,
		CreatedAt: m.CreatedAt,
	}, nil
}

func decodeMessage(d messageDoc) (domain.Message, error) {
	room, thread, seq, err := keys.ParseMsg(d.ID)
	if err != nil {
		return domain.Message{}, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
	}
	pts, err := toUint64("message pts", d.Pts)
	if err != nil {
		return domain.Message{}, err
	}
	return domain.Message{
		Room:      room,
		Thread:    thread,
		Seq:       seq,
		Pts:       pts,
		Tenant:    d.Tenant,
		From:      d.From,
		Kind:      d.Kind,
		Text:      d.Text,
		CID:       d.CID,
		CreatedAt: d.CreatedAt,
	}, nil
}

func decodeMessages(docs []messageDoc) ([]domain.Message, error) {
	out := make([]domain.Message, len(docs))
	for i, d := range docs {
		m, err := decodeMessage(d)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

func encodeRoom(r domain.Room) (roomDoc, error) {
	id, err := toInt64("room id", r.ID)
	if err != nil {
		return roomDoc{}, err
	}
	return roomDoc{
		ID:          id,
		Tenant:      r.Tenant,
		Type:        r.Type,
		Name:        r.Name,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		MemberCount: r.MemberCount,
	}, nil
}

func decodeRoom(d roomDoc) (domain.Room, error) {
	id, err := toUint64("room id", d.ID)
	if err != nil {
		return domain.Room{}, err
	}
	return domain.Room{
		ID:          id,
		Tenant:      d.Tenant,
		Type:        d.Type,
		Name:        d.Name,
		CreatedBy:   d.CreatedBy,
		CreatedAt:   d.CreatedAt,
		MemberCount: d.MemberCount,
	}, nil
}

func encodeMember(m domain.Member, room int64) memberDoc {
	return memberDoc{Room: room, User: m.User, Tenant: m.Tenant, Role: m.Role, JoinedAt: m.JoinedAt}
}

func decodeMember(d memberDoc) (domain.Member, error) {
	room, err := toUint64("member room", d.Room)
	if err != nil {
		return domain.Member{}, err
	}
	return domain.Member{Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt}, nil
}

func toInt64(field string, v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, fmt.Errorf("%w: %s above max int64", apperr.ErrInvalidArgument, field)
	}
	return int64(v), nil
}

func toUint64(field string, v int64) (uint64, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: negative %s", errCorrupt, field)
	}
	return uint64(v), nil
}
