package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxTextBytes = 16384
	maxNameRunes = 128
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
	if typ != RoomGroup {
		return Room{}, nil, invalid("type")
	}
	if err := validateName(name); err != nil {
		return Room{}, nil, err
	}
	users, err := distinctMembers(creator, members)
	if err != nil {
		return Room{}, nil, err
	}
	j := Join{Room: id, Tenant: tenant, RequestID: CreationRequestID(id), By: creator, At: now, Owner: creator}
	room := Room{ID: id, Tenant: tenant, Type: typ, Name: name, CreatedBy: creator, CreatedAt: now, MemberCount: len(users)}
	return room, founders(j, users), nil
}

func NewDirectRoom(tenant, caller, other string, now time.Time, id uint64) (Room, []Member, error) {
	if err := ValidTenant(tenant); err != nil {
		return Room{}, nil, err
	}
	if err := ValidUser(caller); err != nil {
		return Room{}, nil, err
	}
	if err := ValidUser(other); err != nil {
		return Room{}, nil, err
	}
	if caller == other {
		return Room{}, nil, ErrSelfDirect
	}
	j := Join{Room: id, Tenant: tenant, RequestID: CreationRequestID(id), By: caller, At: now}
	room := Room{ID: id, Tenant: tenant, Type: RoomDM, CreatedBy: caller, CreatedAt: now, MemberCount: 2, DMKey: DirectKey(tenant, caller, other)}
	return room, founders(j, []string{caller, other}), nil
}

func founders(j Join, users []string) []Member {
	out := make([]Member, len(users))
	for i, u := range users {
		out[i] = Member{
			Room: j.Room, Tenant: j.Tenant, User: u, Role: j.RoleOf(u), JoinedAt: j.At, State: MemberActive, Ver: 1,
			RequestID: j.RequestID, UpdatedAt: j.At, UpdatedBy: j.By, LastChangeAt: j.At,
		}
	}
	return out
}

func validateName(name string) error {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return invalid("name")
	}
	if strings.TrimSpace(name) == "" {
		return invalid("name")
	}
	return nil
}

func distinctMembers(creator string, members []string) ([]string, error) {
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
	if _, ok := seen[creator]; !ok {
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
