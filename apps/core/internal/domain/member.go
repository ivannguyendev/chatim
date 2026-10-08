package domain

import (
	"strconv"
	"time"
)

type MemberState int32

const (
	MemberActive  MemberState = 1
	MemberRemoved MemberState = 2
)

const MaxMemberBatch = 1000

type Member struct {
	Room             uint64
	Tenant           string
	User             string
	Role             Role
	JoinedAt         time.Time
	ClearedAt        time.Time
	State            MemberState
	Ver              uint32
	Priority         int32
	PreviousRole     Role
	PreviousState    MemberState
	PreviousPriority int32
	RequestID        string
	UpdatedAt        time.Time
	UpdatedBy        string
	LastChangeAt     time.Time
	ReadSeq          uint64
	ReadVer          uint64
}

type Join struct {
	Room      uint64
	Tenant    string
	RequestID string
	By        string
	At        time.Time
	ReadSeq   uint64
}

type MemberCount struct {
	Count int
	Ver   uint64
}

func (m Member) Active() bool { return m.State == MemberActive }

func (m Member) Next(role Role, state MemberState, priority int32, requestID, by string, at time.Time) Member {
	next := m
	next.PreviousRole, next.PreviousState, next.PreviousPriority = m.Role, m.State, m.Priority
	next.Role, next.State, next.Priority = role, state, priority
	next.Ver = m.Ver + 1
	next.RequestID, next.UpdatedBy = requestID, by
	next.UpdatedAt, next.LastChangeAt = at, at
	return next
}

func (j Join) Apply(cur Member, user string) Member {
	if cur.Active() {
		return cur
	}
	next := cur.Next(RoleMember, MemberActive, 0, j.RequestID, j.By, j.At)
	next.Room, next.Tenant, next.User = j.Room, j.Tenant, user
	next.JoinedAt = j.At
	next.ReadSeq = max(cur.ReadSeq, j.ReadSeq)
	next.ReadVer = cur.ReadVer + 1
	return next
}

func AddedBy(m Member, requestID, by string) bool {
	return m.Active() && m.RequestID == requestID && m.UpdatedBy == by
}

func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleOwner, RoleAdmin, RoleMember:
		return r, nil
	default:
		return "", invalid("role")
	}
}

func CreationRequestID(room uint64) string {
	return strconv.FormatUint(room, 10) + "-created"
}
