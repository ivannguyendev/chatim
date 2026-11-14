package domain_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

func TestValidateRoomUpdate(t *testing.T) {
	good := []domain.RoomUpdate{
		{Name: new("Cửa hàng 12")},
		{Avatars: new([]string{"acme/an/pub/20261113/0cr1"})},
		{Avatars: new([]string{})},
		{Name: new(strings.Repeat("ạ", 128)), Avatars: new([]string{"acme/an/pub/20261113/0cr1"})},
	}
	for _, u := range good {
		if err := domain.ValidateRoomUpdate(u); err != nil {
			t.Fatalf("ValidateRoomUpdate(%+v) = %v, want nil", u, err)
		}
	}
	bad := []struct {
		name  string
		u     domain.RoomUpdate
		field string
	}{
		{"nothing", domain.RoomUpdate{}, "update"},
		{"empty name", domain.RoomUpdate{Name: new("")}, "name"},
		{"blank name", domain.RoomUpdate{Name: new("  ")}, "name"},
		{"long name", domain.RoomUpdate{Name: new(strings.Repeat("a", 129))}, "name"},
		{"bad utf-8 name", domain.RoomUpdate{Name: new("hi \xff")}, "name"},
		{"two avatars", domain.RoomUpdate{Avatars: new([]string{"a", "b"})}, "avatars"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) { assertInvalid(t, domain.ValidateRoomUpdate(c.u), c.field) })
	}
}

func TestRoomUpdateApplyBumpsInfoVerOnlyOnChange(t *testing.T) {
	avatar := "acme/an/pub/20261113/0cr1"
	room := domain.Room{ID: 777, Name: "Cửa hàng 12", Avatars: []string{avatar}, InfoVer: 2}
	cases := []struct {
		name    string
		u       domain.RoomUpdate
		want    domain.Room
		changed bool
	}{
		{"same name", domain.RoomUpdate{Name: new("Cửa hàng 12")}, room, false},
		{"same both", domain.RoomUpdate{Name: new("Cửa hàng 12"), Avatars: new([]string{avatar})}, room, false},
		{"rename keeps the avatar", domain.RoomUpdate{Name: new("Kho")}, domain.Room{ID: 777, Name: "Kho", Avatars: []string{avatar}, InfoVer: 3}, true},
		{"remove avatar", domain.RoomUpdate{Avatars: new([]string{})}, domain.Room{ID: 777, Name: "Cửa hàng 12", InfoVer: 3}, true},
		{"new avatar", domain.RoomUpdate{Avatars: new([]string{"acme/an/pub/20261113/0cr2"})}, domain.Room{ID: 777, Name: "Cửa hàng 12", Avatars: []string{"acme/an/pub/20261113/0cr2"}, InfoVer: 3}, true},
	}
	for _, c := range cases {
		got, changed := c.u.Apply(room)
		if changed != c.changed || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Apply = %+v, %v; want %+v, %v", c.name, got, changed, c.want, c.changed)
		}
	}
}

func TestRoomUpdateApplyCopiesTheAvatarKeys(t *testing.T) {
	keys := []string{"acme/an/pub/20261113/0cr1"}
	got, _ := domain.RoomUpdate{Avatars: &keys}.Apply(domain.Room{ID: 777})
	keys[0] = "changed"
	if got.Avatars[0] != "acme/an/pub/20261113/0cr1" {
		t.Fatalf("avatars = %v, want a copy of the update", got.Avatars)
	}
}

func TestDirectUsersSplitsTheDirectKey(t *testing.T) {
	dm := domain.Room{Tenant: "acme", Type: domain.RoomDM, DMKey: domain.DirectKey("acme", "minh", "lan")}
	if got := dm.DirectUsers(); !reflect.DeepEqual(got, []string{"lan", "minh"}) {
		t.Fatalf("DirectUsers = %v, want [lan minh]", got)
	}
	none := []domain.Room{
		{Tenant: "acme", Type: domain.RoomGroup, DMKey: domain.DirectKey("acme", "minh", "lan")},
		{Tenant: "acme", Type: domain.RoomDM},
		{Tenant: "acme", Type: domain.RoomDM, DMKey: domain.DirectKey("other", "minh", "lan")},
		{Tenant: "acme", Type: domain.RoomDM, DMKey: "acme│minh"},
		{Tenant: "acme", Type: domain.RoomDM, DMKey: "acme││lan"},
	}
	for _, r := range none {
		if got := r.DirectUsers(); got != nil {
			t.Errorf("DirectUsers(%+v) = %v, want nil", r, got)
		}
	}
}
