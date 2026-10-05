package domain

import "time"

type RoomType string

const (
	RoomDM    RoomType = "dm"
	RoomGroup RoomType = "group"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

type Room struct {
	ID           uint64
	Tenant       string
	Type         RoomType
	Name         string
	CreatedBy    string
	CreatedAt    time.Time
	MemberCount  int
	LastSeq      uint64
	LastMsgAt    time.Time
	LastChangeAt time.Time
}

type Member struct {
	Room             uint64
	Tenant           string
	User             string
	Role             Role
	JoinedAt         time.Time
	ClearedBeforeSeq uint64
}

func ParseRoomType(s string) (RoomType, error) {
	switch t := RoomType(s); t {
	case RoomDM, RoomGroup:
		return t, nil
	default:
		return "", invalid("type")
	}
}
