package store_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestOutcomeString(t *testing.T) {
	tests := map[store.Outcome]string{
		store.Inserted:  "inserted",
		store.Duplicate: "duplicate",
		store.Unknown:   "unknown",
		store.Rejected:  "rejected",
		0:               "outcome(0)",
		9:               "outcome(9)",
	}
	for o, want := range tests {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", uint8(o), got, want)
		}
	}
}

func TestKeyOf(t *testing.T) {
	m := domain.Message{Room: 42, Thread: 7, Seq: 3, Text: "hi"}
	if got, want := store.KeyOf(m), (store.MsgKey{Room: 42, Thread: 7, Seq: 3}); got != want {
		t.Fatalf("KeyOf = %+v, want %+v", got, want)
	}
}

func TestMsgKeyValidate(t *testing.T) {
	tests := []struct {
		key store.MsgKey
		ok  bool
	}{
		{store.MsgKey{Room: 1, Seq: 1}, true},
		{store.MsgKey{Room: 1, Thread: 7, Seq: 9}, true},
		{store.MsgKey{Room: 0, Seq: 1}, false},
		{store.MsgKey{Room: 1, Seq: 0}, false},
		{store.MsgKey{}, false},
	}
	for _, tt := range tests {
		assertValid(t, tt.key.Validate(), tt.ok, tt.key)
	}
}

func TestValidateKeys(t *testing.T) {
	tests := []struct {
		room uint64
		keys []store.MsgKey
		ok   bool
	}{
		{42, nil, true},
		{42, []store.MsgKey{{Room: 42, Seq: 1}, {Room: 42, Thread: 7, Seq: 2}}, true},
		{42, []store.MsgKey{{Room: 42, Seq: 1}, {Room: 43, Seq: 1}}, false},
		{42, []store.MsgKey{{Room: 0, Seq: 1}}, false},
	}
	for _, tt := range tests {
		assertValid(t, store.ValidateKeys(tt.room, tt.keys), tt.ok, tt.keys)
	}
}

func TestPageQueryValidate(t *testing.T) {
	tests := []struct {
		q  store.PageQuery
		ok bool
	}{
		{store.PageQuery{Room: 1, Anchor: store.Latest, Limit: 1}, true},
		{store.PageQuery{Room: 1, Anchor: store.Oldest, Limit: store.MaxPageLimit}, true},
		{store.PageQuery{Room: 1, Anchor: store.Before, Seq: 5, Limit: 50}, true},
		{store.PageQuery{Room: 1, Anchor: store.After, Seq: 5, Limit: 50}, true},
		{store.PageQuery{Room: 1, Anchor: store.Latest, Limit: 0}, false},
		{store.PageQuery{Room: 1, Anchor: store.Latest, Limit: -1}, false},
		{store.PageQuery{Room: 1, Anchor: store.Latest, Limit: store.MaxPageLimit + 1}, false},
		{store.PageQuery{Room: 1, Anchor: 0, Limit: 10}, false},
		{store.PageQuery{Room: 1, Anchor: store.After + 1, Limit: 10}, false},
	}
	for _, tt := range tests {
		assertValid(t, tt.q.Validate(), tt.ok, tt.q)
	}
}

func TestValidateRoom(t *testing.T) {
	room := domain.Room{ID: 42, Tenant: "acme"}
	owner := domain.Member{Room: 42, Tenant: "acme", User: "alice"}
	other := domain.Member{Room: 42, Tenant: "acme", User: "bob"}
	tests := []struct {
		name    string
		room    domain.Room
		members []domain.Member
		ok      bool
	}{
		{"valid", room, []domain.Member{owner, other}, true},
		{"zero id", domain.Room{Tenant: "acme"}, []domain.Member{{Tenant: "acme", User: "alice"}}, false},
		{"empty tenant", domain.Room{ID: 42}, []domain.Member{{Room: 42, User: "alice"}}, false},
		{"nil members", room, nil, false},
		{"empty members", room, []domain.Member{}, false},
		{"member of another room", room, []domain.Member{owner, {Room: 43, Tenant: "acme", User: "bob"}}, false},
		{"member of another tenant", room, []domain.Member{owner, {Room: 42, Tenant: "other", User: "bob"}}, false},
		{"member without tenant", room, []domain.Member{{Room: 42, User: "alice"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValid(t, store.ValidateRoom(tt.room, tt.members), tt.ok, tt.name)
		})
	}
}

func assertValid(t *testing.T, err error, ok bool, input any) {
	t.Helper()
	if ok {
		if err != nil {
			t.Errorf("validate(%+v) = %v, want nil", input, err)
		}
		return
	}
	if !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Errorf("validate(%+v) = %v, want ErrInvalidArgument", input, err)
	}
}
