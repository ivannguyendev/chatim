package domain

import (
	"slices"
	"strings"
	"time"
)

const (
	directKeySeparator = "│"
	MaxAvatarUsers     = 4
)

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
	LastEditAt     time.Time
	MemberCountVer uint64
	DMKey          string
	Avatars        []string
	InfoVer        uint64
	UpdatedAt      time.Time
	UpdatedBy      string
	AvatarUsers    []string
	AvatarVer      uint64
}

type RoomUpdate struct {
	Name    *string
	Avatars *[]string
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

func (r Room) DirectUsers() []string {
	if r.Type != RoomDM {
		return nil
	}
	parts := strings.Split(r.DMKey, directKeySeparator)
	if len(parts) != 3 || parts[0] != r.Tenant || parts[1] == "" || parts[2] == "" {
		return nil
	}
	return parts[1:]
}

func ValidateRoomUpdate(u RoomUpdate) error {
	if u.Name == nil && u.Avatars == nil {
		return invalid("update")
	}
	if u.Name != nil {
		if err := validateName(*u.Name); err != nil {
			return err
		}
	}
	if u.Avatars != nil && len(*u.Avatars) > MaxRoomAvatars {
		return invalid("avatars")
	}
	return nil
}

func (u RoomUpdate) Apply(r Room) (Room, bool) {
	next := r
	if u.Name != nil {
		next.Name = *u.Name
	}
	if u.Avatars != nil {
		next.Avatars = cloneKeys(*u.Avatars)
	}
	if next.Name == r.Name && slices.Equal(next.Avatars, r.Avatars) {
		return r, false
	}
	next.InfoVer = r.InfoVer + 1
	return next, true
}

func cloneKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	return slices.Clone(keys)
}
