package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func wantInvalid(t *testing.T, name string, err error) {
	t.Helper()
	if !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("%s = %v, want ErrInvalidArgument", name, err)
	}
}

func TestValidateRoomCalls(t *testing.T) {
	if err := store.ValidateRaise("acme", 7, at(1)); err != nil {
		t.Fatalf("ValidateRaise(good) = %v", err)
	}
	wantInvalid(t, "raise bad tenant", store.ValidateRaise("", 7, at(1)))
	wantInvalid(t, "raise room 0", store.ValidateRaise("acme", 0, at(1)))
	wantInvalid(t, "raise room over int64", store.ValidateRaise("acme", 1<<63, at(1)))
	wantInvalid(t, "raise zero time", store.ValidateRaise("acme", 7, time.Time{}))
	if err := store.ValidateRooms("acme", nil); err != nil {
		t.Fatalf("ValidateRooms(nil) = %v", err)
	}
	wantInvalid(t, "rooms with 0", store.ValidateRooms("acme", []uint64{7, 0}))
	wantInvalid(t, "rooms bad tenant", store.ValidateRooms("A B", []uint64{7}))
}

func TestValidateGroupReads(t *testing.T) {
	if err := store.ValidateGroupMembersQuery("acme", "team:ops", "", store.MaxGroupMemberPage); err != nil {
		t.Fatalf("ValidateGroupMembersQuery(good) = %v", err)
	}
	wantInvalid(t, "members bad group", store.ValidateGroupMembersQuery("acme", "a b", "", 10))
	wantInvalid(t, "members bad after", store.ValidateGroupMembersQuery("acme", "ops", "a b", 10))
	wantInvalid(t, "members zero limit", store.ValidateGroupMembersQuery("acme", "ops", "", 0))
	wantInvalid(t, "members over page", store.ValidateGroupMembersQuery("acme", "ops", "", store.MaxGroupMemberPage+1))
	wantInvalid(t, "users bad user", store.ValidateUsers("acme", []string{"ha", ""}))
	wantInvalid(t, "mentions of bad key", store.ValidateMentionsOf("acme", domain.MessageKey{Room: 7}))
}

func TestValidateGroupWrites(t *testing.T) {
	ha := domain.UserGroups{Tenant: "acme", User: "ha", Groups: []string{"ops"}, Ver: 1}
	other := ha
	other.Tenant = "globex"
	if err := store.ValidateUserGroups([]domain.UserGroups{ha, other}); err != nil {
		t.Fatalf("ValidateUserGroups(two tenants) = %v", err)
	}
	wantInvalid(t, "user twice", store.ValidateUserGroups([]domain.UserGroups{ha, ha}))
	wantInvalid(t, "bad user groups", store.ValidateUserGroups([]domain.UserGroups{{Tenant: "acme", User: "ha"}}))
	g := domain.Group{Tenant: "acme", ID: "ops", Name: "Ops", Ver: 1}
	if err := store.ValidateGroups([]domain.Group{g}); err != nil {
		t.Fatalf("ValidateGroups(good) = %v", err)
	}
	wantInvalid(t, "group twice", store.ValidateGroups([]domain.Group{g, g}))
	wantInvalid(t, "bad group", store.ValidateGroups([]domain.Group{{Tenant: "acme", ID: "ops", Ver: 1}}))
}
