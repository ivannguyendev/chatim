package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTextBytes = 16384
	maxNameRunes = 128
	dmMembers    = 2
	defaultPage  = 50
	maxPage      = 100
)

func NewRoom(tenant, creator string, typ RoomType, name string, members []string, now time.Time, id uint64) (Room, []Member, error) {
	if err := ValidTenant(tenant); err != nil {
		return Room{}, nil, err
	}
	if err := ValidUser(creator); err != nil {
		return Room{}, nil, err
	}
	if _, err := ParseRoomType(string(typ)); err != nil {
		return Room{}, nil, err
	}
	if err := validateName(typ, name); err != nil {
		return Room{}, nil, err
	}
	users, err := distinctMembers(typ, creator, members)
	if err != nil {
		return Room{}, nil, err
	}
	requestID := CreationRequestID(id)
	out := make([]Member, len(users))
	for i, u := range users {
		role := RoleMember
		if u == creator {
			role = RoleOwner
		}
		out[i] = Member{
			Room: id, Tenant: tenant, User: u, Role: role, JoinedAt: now, State: MemberActive, Ver: 1,
			RequestID: requestID, UpdatedAt: now, UpdatedBy: creator, LastChangeAt: now,
		}
	}
	room := Room{ID: id, Tenant: tenant, Type: typ, Name: name, CreatedBy: creator, CreatedAt: now, MemberCount: len(out)}
	return room, out, nil
}

func validateName(typ RoomType, name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return invalid("name")
	}
	if typ == RoomGroup && strings.TrimSpace(name) == "" {
		return invalid("name")
	}
	return nil
}

func distinctMembers(typ RoomType, creator string, members []string) ([]string, error) {
	seen := make(map[string]struct{}, len(members))
	users := make([]string, 0, len(members))
	for _, u := range members {
		if err := ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		users = append(users, u)
	}
	if _, ok := seen[creator]; !ok || (typ == RoomDM && len(users) != dmMembers) {
		return nil, invalid("members")
	}
	return users, nil
}

func ValidateText(text string) error {
	if len(text) > maxTextBytes || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return invalid("text")
	}
	return nil
}

func ValidateThread(thread uint64) error {
	if thread != 0 {
		return invalid("thread")
	}
	return nil
}

func PageLimit(n int) (int, error) {
	switch {
	case n == 0:
		return defaultPage, nil
	case n < 0 || n > maxPage:
		return 0, invalid("limit")
	default:
		return n, nil
	}
}
