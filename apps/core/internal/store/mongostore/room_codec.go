package mongostore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

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
	LastEditAt     time.Time       `bson:"last_edit_at,omitempty"`
	DMKey          string          `bson:"dm_key,omitempty"`
	Avatar         string          `bson:"avatar,omitempty"`
	InfoVer        int64           `bson:"info_ver,omitempty"`
	UpdatedAt      time.Time       `bson:"updated_at,omitempty"`
	UpdatedBy      string          `bson:"updated_by,omitempty"`

	LastSharedMentionAt time.Time `bson:"last_shared_mention_at,omitempty"`
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
	infoVer, err := toInt64("info ver", r.InfoVer)
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
		DMKey:          r.DMKey,
		Avatar:         r.Avatar,
		InfoVer:        infoVer,
		UpdatedAt:      r.UpdatedAt,
		UpdatedBy:      r.UpdatedBy,
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
	infoVer, err := toUint64("room info ver", d.InfoVer)
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
		LastEditAt:     d.LastEditAt,
		MemberCountVer: memberCountVer,
		DMKey:          d.DMKey,
		Avatar:         d.Avatar,
		InfoVer:        infoVer,
		UpdatedAt:      d.UpdatedAt,
		UpdatedBy:      d.UpdatedBy,

		LastSharedMentionAt: d.LastSharedMentionAt,
	}, nil
}
