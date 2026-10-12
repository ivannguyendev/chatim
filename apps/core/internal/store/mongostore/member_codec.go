package mongostore

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type memberDoc struct {
	ID               []byte      `bson:"_id"`
	Room             int64       `bson:"room_id"`
	Tenant           string      `bson:"tenant"`
	User             string      `bson:"user_id"`
	Role             domain.Role `bson:"role"`
	State            int64       `bson:"state"`
	Priority         int64       `bson:"priority"`
	JoinedAt         time.Time   `bson:"joined_at"`
	Ver              int64       `bson:"ver"`
	PreviousRole     domain.Role `bson:"previous_role"`
	PreviousState    int64       `bson:"previous_state"`
	PreviousPriority int64       `bson:"previous_priority"`
	RequestID        string      `bson:"request_id"`
	UpdatedAt        time.Time   `bson:"updated_at"`
	UpdatedBy        string      `bson:"updated_by"`
	LastChangeAt     time.Time   `bson:"last_change_at"`
	ClearedAt        time.Time   `bson:"cleared_at,omitempty"`
	ReadSeq          int64       `bson:"read_seq"`
	ReadVer          int64       `bson:"read_ver"`
}

type readPositionDoc struct {
	ReadSeq int64 `bson:"read_seq"`
	ReadVer int64 `bson:"read_ver"`
}

func encodeMember(m domain.Member) (memberDoc, error) {
	room, err := toInt64("member room", m.Room)
	if err != nil {
		return memberDoc{}, err
	}
	readSeq, err := toInt64("member read seq", m.ReadSeq)
	if err != nil {
		return memberDoc{}, err
	}
	readVer, err := toInt64("member read ver", m.ReadVer)
	if err != nil {
		return memberDoc{}, err
	}
	return memberDoc{
		ID: keys.Member(m.Room, m.User), Room: room, Tenant: m.Tenant, User: m.User,
		Role: m.Role, State: int64(m.State), Priority: int64(m.Priority), JoinedAt: m.JoinedAt, Ver: int64(m.Ver),
		PreviousRole: m.PreviousRole, PreviousState: int64(m.PreviousState), PreviousPriority: int64(m.PreviousPriority),
		RequestID: m.RequestID, UpdatedAt: m.UpdatedAt, UpdatedBy: m.UpdatedBy, LastChangeAt: m.LastChangeAt,
		ClearedAt: m.ClearedAt, ReadSeq: readSeq, ReadVer: readVer,
	}, nil
}

func decodeMember(d memberDoc) (domain.Member, error) {
	room, err := toUint64("member room", d.Room)
	if err != nil {
		return domain.Member{}, err
	}
	pos, err := decodeReadPosition(readPositionDoc{ReadSeq: d.ReadSeq, ReadVer: d.ReadVer})
	if err != nil {
		return domain.Member{}, err
	}
	state, err := memberState("member state", d.State, int64(domain.MemberActive))
	if err != nil {
		return domain.Member{}, err
	}
	previousState, err := memberState("member previous state", d.PreviousState, 0)
	if err != nil {
		return domain.Member{}, err
	}
	priority, err := int32Of("member priority", d.Priority)
	if err != nil {
		return domain.Member{}, err
	}
	previousPriority, err := int32Of("member previous priority", d.PreviousPriority)
	if err != nil {
		return domain.Member{}, err
	}
	if d.Ver < 0 || d.Ver > math.MaxUint32 {
		return domain.Member{}, fmt.Errorf("%w: member ver %d", errCorrupt, d.Ver)
	}
	return domain.Member{
		Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedAt: d.ClearedAt,
		State: state, Ver: uint32(d.Ver), Priority: priority,
		PreviousRole: d.PreviousRole, PreviousState: previousState, PreviousPriority: previousPriority,
		RequestID: d.RequestID, UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy, LastChangeAt: d.LastChangeAt,
		ReadSeq: pos.Seq, ReadVer: pos.Ver,
	}, nil
}

func decodeMembers(docs []memberDoc) ([]domain.Member, error) {
	out := make([]domain.Member, len(docs))
	for i, d := range docs {
		m, err := decodeMember(d)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

func decodeReadPosition(d readPositionDoc) (domain.ReadPosition, error) {
	seq, err := toUint64("member read seq", d.ReadSeq)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	ver, err := toUint64("member read ver", d.ReadVer)
	if err != nil {
		return domain.ReadPosition{}, err
	}
	return domain.ReadPosition{Seq: seq, Ver: ver}, nil
}

func memberState(field string, v, lowest int64) (domain.MemberState, error) {
	n, err := int32Of(field, v)
	if err != nil || int64(n) < lowest || n > int32(domain.MemberRemoved) {
		return 0, fmt.Errorf("%w: %s %d", errCorrupt, field, v)
	}
	return domain.MemberState(n), nil
}

func int32Of(field string, v int64) (int32, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s %d outside int32", errCorrupt, field, v)
	}
	return int32(v), nil
}
