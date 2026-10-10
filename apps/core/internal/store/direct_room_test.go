package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestValidateDirectClaim(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_000)
	if err := store.ValidateDirectClaim("acme", "lan", "minh", 8812, at); err != nil {
		t.Fatalf("ValidateDirectClaim(good) = %v", err)
	}
	cases := map[string]error{
		"bad tenant": store.ValidateDirectClaim("Acme", "lan", "minh", 8812, at),
		"bad user":   store.ValidateDirectClaim("acme", "l n", "minh", 8812, at),
		"self":       store.ValidateDirectClaim("acme", "lan", "lan", 8812, at),
		"zero room":  store.ValidateDirectClaim("acme", "lan", "minh", 0, at),
		"zero time":  store.ValidateDirectClaim("acme", "lan", "minh", 8812, time.Time{}),
	}
	for name, err := range cases {
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateDirectClaim = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestValidateRoomInsert(t *testing.T) {
	group := domain.Room{ID: 9, Tenant: "acme", Type: domain.RoomGroup, Name: "Team"}
	dm := domain.Room{ID: 9, Tenant: "acme", Type: domain.RoomDM, DMKey: domain.DirectKey("acme", "lan", "minh")}
	for name, r := range map[string]domain.Room{"group": group, "dm": dm} {
		if err := store.ValidateRoomInsert(r); err != nil {
			t.Errorf("ValidateRoomInsert(%s) = %v", name, err)
		}
	}
	edit := func(r domain.Room, f func(*domain.Room)) domain.Room {
		f(&r)
		return r
	}
	cases := map[string]domain.Room{
		"zero id":           edit(group, func(r *domain.Room) { r.ID = 0 }),
		"bad tenant":        edit(group, func(r *domain.Room) { r.Tenant = "" }),
		"unknown type":      edit(group, func(r *domain.Room) { r.Type = "channel" }),
		"group with dm key": edit(group, func(r *domain.Room) { r.DMKey = dm.DMKey }),
		"dm without key":    edit(dm, func(r *domain.Room) { r.DMKey = "" }),
	}
	for name, r := range cases {
		if err := store.ValidateRoomInsert(r); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateRoomInsert = %v, want ErrInvalidArgument", name, err)
		}
	}
}
