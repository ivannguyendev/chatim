package mongostore

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var errCorrupt = errors.New("mongostore: corrupt document")

type messageDoc struct {
	ID        []byte        `bson:"_id"`
	Tenant    string        `bson:"t"`
	From      string        `bson:"f"`
	Kind      domain.Kind   `bson:"k"`
	Text      string        `bson:"x"`
	CID       string        `bson:"c"`
	CreatedAt time.Time     `bson:"ts"`
	Version   int32         `bson:"v,omitempty"`
	Deleted   bool          `bson:"d,omitempty"`
	EditedAt  time.Time     `bson:"ea,omitempty"`
	Reactions *reactionsDoc `bson:"rx,omitempty"`
}

type roomDoc struct {
	ID             int64           `bson:"_id"`
	Tenant         string          `bson:"tenant"`
	Type           domain.RoomType `bson:"type"`
	Name           string          `bson:"name"`
	CreatedBy      string          `bson:"created_by"`
	CreatedAt      time.Time       `bson:"created_at"`
	MemberCount    int             `bson:"member_count"`
	MemberCountVer int64           `bson:"member_count_ver,omitempty"`
	LastSeq        int64           `bson:"last_seq,omitempty"`
	LastMsgAt      time.Time       `bson:"last_message_at,omitempty"`
	LastChangeAt   time.Time       `bson:"last_change_at,omitempty"`
}

func encodeMessage(m domain.Message) (messageDoc, error) {
	if err := store.KeyOf(m).Validate(); err != nil {
		return messageDoc{}, err
	}
	if _, err := toInt64("seq", m.Seq); err != nil {
		return messageDoc{}, err
	}
	version, err := toInt32("version", m.Version)
	if err != nil {
		return messageDoc{}, err
	}
	return messageDoc{
		ID:        keys.Msg(m.Room, m.Thread, m.Seq),
		Tenant:    m.Tenant,
		From:      m.From,
		Kind:      m.Kind,
		Text:      m.Text,
		CID:       m.CID,
		CreatedAt: m.CreatedAt,
		Version:   version,
		Deleted:   m.Deleted,
		EditedAt:  m.EditedAt,
	}, nil
}

func decodeMessage(d messageDoc) (domain.Message, error) {
	room, thread, seq, err := keys.ParseMsg(d.ID)
	if err != nil {
		return domain.Message{}, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
	}
	version, err := toUint32("message version", d.Version)
	if err != nil {
		return domain.Message{}, err
	}
	reactions, err := decodeSummary(d.Reactions)
	if err != nil {
		return domain.Message{}, err
	}
	return domain.Message{
		Room:      room,
		Thread:    thread,
		Seq:       seq,
		Tenant:    d.Tenant,
		From:      d.From,
		Kind:      d.Kind,
		Text:      d.Text,
		CID:       d.CID,
		CreatedAt: d.CreatedAt,
		Version:   version,
		Deleted:   d.Deleted,
		EditedAt:  d.EditedAt,
		Reactions: reactions,
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
	memberCountVer, err := toInt64("member count ver", r.MemberCountVer)
	if err != nil {
		return roomDoc{}, err
	}
	return roomDoc{
		ID:             id,
		Tenant:         r.Tenant,
		Type:           r.Type,
		Name:           r.Name,
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt,
		MemberCount:    r.MemberCount,
		MemberCountVer: memberCountVer,
	}, nil
}

func decodeRoom(d roomDoc) (domain.Room, error) {
	id, err := toUint64("room id", d.ID)
	if err != nil {
		return domain.Room{}, err
	}
	lastSeq, err := toUint64("room last seq", d.LastSeq)
	if err != nil {
		return domain.Room{}, err
	}
	memberCountVer, err := toUint64("room member count ver", d.MemberCountVer)
	if err != nil {
		return domain.Room{}, err
	}
	return domain.Room{
		ID:             id,
		Tenant:         d.Tenant,
		Type:           d.Type,
		Name:           d.Name,
		CreatedBy:      d.CreatedBy,
		CreatedAt:      d.CreatedAt,
		MemberCount:    d.MemberCount,
		LastSeq:        lastSeq,
		LastMsgAt:      d.LastMsgAt,
		LastChangeAt:   d.LastChangeAt,
		MemberCountVer: memberCountVer,
	}, nil
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
