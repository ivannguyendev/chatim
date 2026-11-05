package domain

import (
	"math"
	"time"
)

type Action string

const (
	ActionReadMembers Action = "read_members"
	ActionReadHistory Action = "read_history"
)

type MessageKey struct {
	Room   uint64
	Thread uint64
	Seq    uint64
}

func (k MessageKey) Validate() error {
	if k.Room == 0 || k.Room > math.MaxInt64 || k.Seq == 0 {
		return invalid("message key")
	}
	return nil
}

type Member struct {
	User     string
	Role     string
	Priority int32
	JoinedAt time.Time
	Active   bool
}

func (m Member) Cursor() MemberCursor {
	return MemberCursor{Role: m.Role, Priority: m.Priority, JoinedAt: m.JoinedAt, User: m.User}
}

type MemberCursor struct {
	Role     string
	Priority int32
	JoinedAt time.Time
	User     string
}

type MemberRoom struct {
	Room      uint64
	JoinedAt  time.Time
	ClearedAt time.Time
	ReadSeq   uint64
}
