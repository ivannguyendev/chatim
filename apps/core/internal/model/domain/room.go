package domain

import "time"

const directKeySeparator = "│"

type RoomType string

const (
	RoomDM    RoomType = "dm"
	RoomGroup RoomType = "group"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type Room struct {
	ID             uint64
	Tenant         string
	Type           RoomType
	Name           string
	CreatedBy      string
	CreatedAt      time.Time
	MemberCount    int
	LastSeq        uint64
	LastMsgAt      time.Time
	LastChangeAt   time.Time
	MemberCountVer uint64
	DMKey          string
}

func DirectKey(tenant, a, b string) string {
	return tenant + directKeySeparator + min(a, b) + directKeySeparator + max(a, b)
}

func ParseRoomType(s string) (RoomType, error) {
	switch t := RoomType(s); t {
	case RoomDM, RoomGroup:
		return t, nil
	default:
		return "", invalid("type")
	}
}
