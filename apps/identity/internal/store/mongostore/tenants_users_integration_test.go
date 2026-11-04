package mongostore

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var itAt = time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)

type tenantDoc struct {
	ID                string     `bson:"_id"`
	Name              string     `bson:"name"`
	State             int32      `bson:"state"`
	Ver               int64      `bson:"ver"`
	GroupTreeVer      int64      `bson:"group_tree_ver"`
	SessionsRevokedAt *time.Time `bson:"sessions_revoked_at"`
	CreatedAt         time.Time  `bson:"created_at"`
	UpdatedAt         time.Time  `bson:"updated_at"`
	UpdatedBy         string     `bson:"updated_by"`
}

type userDoc struct {
	ID          string    `bson:"_id"`
	TenantID    string    `bson:"tenant_id"`
	UserID      string    `bson:"id"`
	DisplayName string    `bson:"display_name"`
	AvatarURL   string    `bson:"avatar_url"`
	State       int32     `bson:"state"`
	Groups      bson.A    `bson:"groups"`
	Ver         int64     `bson:"ver"`
	CreatedAt   time.Time `bson:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at"`
	UpdatedBy   string    `bson:"updated_by"`
}

func readTenant(t *testing.T, s *Store, id string) tenantDoc {
	t.Helper()
	var doc tenantDoc
	if err := s.db.Collection(tenantsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: id}}).Decode(&doc); err != nil {
		t.Fatalf("read tenant %s: %v", id, err)
	}
	return doc
}

func readUserDoc(t *testing.T, s *Store, tenantID, id string) userDoc {
	t.Helper()
	var doc userDoc
	err := s.db.Collection(usersCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: tenantID + "│" + id}}).Decode(&doc)
	if err != nil {
		t.Fatalf("read user %s/%s: %v", tenantID, id, err)
	}
	return doc
}

func serverNow(t *testing.T, s *Store) time.Time {
	t.Helper()
	cur, err := s.db.Collection(tenantsCollection).Aggregate(t.Context(), mongo.Pipeline{
		{{Key: "$limit", Value: 1}},
		{{Key: "$project", Value: bson.D{{Key: "now", Value: "$$NOW"}}}},
	})
	if err != nil {
		t.Fatalf("aggregate $$NOW: %v", err)
	}
	var out []struct {
		Now time.Time `bson:"now"`
	}
	if err := cur.All(t.Context(), &out); err != nil || len(out) != 1 {
		t.Fatalf("read $$NOW = %v, %v; want one row", out, err)
	}
	return out[0].Now
}

func mustCreateTenant(t *testing.T, s *Store, id string) {
	t.Helper()
	tenant := store.Tenant{ID: id, Name: "Shop " + id, State: store.StateActive, CreatedAt: itAt, UpdatedAt: itAt, UpdatedBy: "cli:admin"}
	if err := s.CreateTenant(t.Context(), tenant); err != nil {
		t.Fatalf("CreateTenant %s: %v", id, err)
	}
}

func TestCreateTenantInsertsOnceAndRefusesADuplicate(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	got := readTenant(t, s, "shop")
	want := tenantDoc{ID: "shop", Name: "Shop shop", State: 1, Ver: 1, CreatedAt: itAt, UpdatedAt: itAt, UpdatedBy: "cli:admin"}
	if got.SessionsRevokedAt != nil || got.ID != want.ID || got.Name != want.Name || got.State != want.State || got.Ver != want.Ver ||
		got.GroupTreeVer != 0 || !got.CreatedAt.Equal(itAt) || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != want.UpdatedBy {
		t.Fatalf("tenant = %+v; want %+v", got, want)
	}
	err := s.CreateTenant(t.Context(), store.Tenant{ID: "shop", Name: "Other", State: store.StateActive, CreatedAt: itAt, UpdatedAt: itAt})
	if !errors.Is(err, apperr.ErrAlreadyExists) {
		t.Fatalf("CreateTenant twice = %v; want already exists", err)
	}
	if again := readTenant(t, s, "shop"); again.Name != "Shop shop" || again.Ver != 1 {
		t.Fatalf("tenant after the duplicate = %+v; want it unchanged", again)
	}
}

func TestSetTenantStateRevokesOnlyWhenTheTenantGoesFromActiveToDisabled(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	disableAt := itAt.Add(time.Minute)
	before := serverNow(t, s)
	if err := s.SetTenantState(t.Context(), "shop", store.StateDisabled, store.Revocation{By: "cli:ops", Reason: "fraud", At: disableAt}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	after := serverNow(t, s)
	disabled := readTenant(t, s, "shop")
	if disabled.State != 2 || disabled.Ver != 2 || !disabled.UpdatedAt.Equal(disableAt) || disabled.UpdatedBy != "cli:ops" {
		t.Fatalf("disabled tenant = %+v; want state 2, ver 2, updated at %v by cli:ops", disabled, disableAt)
	}
	if mark := disabled.SessionsRevokedAt; mark == nil || mark.Before(before) || mark.After(after) {
		t.Fatalf("sessions_revoked_at = %v; want server time within [%v, %v]", mark, before, after)
	}
	if err := s.SetTenantState(t.Context(), "shop", store.StateDisabled, store.Revocation{By: "cli:other", At: disableAt.Add(time.Minute)}); err != nil {
		t.Fatalf("disable again: %v", err)
	}
	if again := readTenant(t, s, "shop"); !sameTenant(again, disabled) {
		t.Fatalf("tenant after a second disable = %+v; want %+v unchanged", again, disabled)
	}
	enableAt := disableAt.Add(time.Hour)
	if err := s.SetTenantState(t.Context(), "shop", store.StateActive, store.Revocation{By: "cli:ops", At: enableAt}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	enabled := readTenant(t, s, "shop")
	if enabled.State != 1 || enabled.Ver != 3 || enabled.GroupTreeVer != 0 || !enabled.UpdatedAt.Equal(enableAt) {
		t.Fatalf("enabled tenant = %+v; want state 1, ver 3, group_tree_ver 0, updated at %v", enabled, enableAt)
	}
	if enabled.SessionsRevokedAt == nil || !enabled.SessionsRevokedAt.Equal(*disabled.SessionsRevokedAt) {
		t.Fatalf("sessions_revoked_at after enable = %v; want %v kept", enabled.SessionsRevokedAt, disabled.SessionsRevokedAt)
	}
}

func TestSetTenantStateOfAnActiveTenantToActiveWritesNothing(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	if err := s.SetTenantState(t.Context(), "shop", store.StateActive, store.Revocation{By: "cli:ops", At: itAt.Add(time.Hour)}); err != nil {
		t.Fatalf("enable an active tenant: %v", err)
	}
	got := readTenant(t, s, "shop")
	if got.Ver != 1 || got.SessionsRevokedAt != nil || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != "cli:admin" {
		t.Fatalf("tenant = %+v; want it unchanged", got)
	}
}

func TestSetTenantStateOfAnUnknownTenantIsNotFound(t *testing.T) {
	s := testDatabase(t)
	err := s.SetTenantState(t.Context(), "ghost", store.StateDisabled, store.Revocation{By: "cli:ops", At: itAt})
	if !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("SetTenantState = %v; want not found", err)
	}
}

func sameTenant(a, b tenantDoc) bool {
	return a.ID == b.ID && a.Name == b.Name && a.State == b.State && a.Ver == b.Ver && a.GroupTreeVer == b.GroupTreeVer &&
		a.SessionsRevokedAt != nil && b.SessionsRevokedAt != nil && a.SessionsRevokedAt.Equal(*b.SessionsRevokedAt) &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.UpdatedBy == b.UpdatedBy
}

func TestSetUserStateBumpsTheVersionOnlyOnAChange(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	disableAt := itAt.Add(time.Minute)
	if err := s.SetUserState(t.Context(), "shop", "lan", store.StateDisabled, disableAt, "cli:ops"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled := readUserDoc(t, s, "shop", "lan")
	if disabled.State != 2 || disabled.Ver != 2 || !disabled.UpdatedAt.Equal(disableAt) || disabled.UpdatedBy != "cli:ops" {
		t.Fatalf("disabled user = %+v; want state 2, ver 2, updated at %v by cli:ops", disabled, disableAt)
	}
	if err := s.SetUserState(t.Context(), "shop", "lan", store.StateDisabled, disableAt.Add(time.Minute), "cli:other"); err != nil {
		t.Fatalf("disable again: %v", err)
	}
	if again := readUserDoc(t, s, "shop", "lan"); again.Ver != 2 || !again.UpdatedAt.Equal(disableAt) || again.UpdatedBy != "cli:ops" {
		t.Fatalf("user after a second disable = %+v; want ver 2 updated at %v by cli:ops", again, disableAt)
	}
}

func TestSetUserStateOfAnUnknownUserIsNotFound(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	for _, c := range []struct{ tenant, user string }{{"shop", "ghost"}, {"other", "lan"}} {
		if err := s.SetUserState(t.Context(), c.tenant, c.user, store.StateDisabled, itAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("SetUserState %s/%s = %v; want not found", c.tenant, c.user, err)
		}
	}
}

func TestSetProfileBumpsTheVersionOnlyWhenAValueChanges(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "https://cdn.example/lan.png")
	renameAt := itAt.Add(time.Minute)
	name := "Lan Nguyen"
	if err := s.SetProfile(t.Context(), "shop", "lan", store.ProfileUpdate{DisplayName: &name}, renameAt, "cli:ops"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renamed := readUserDoc(t, s, "shop", "lan")
	if renamed.DisplayName != name || renamed.AvatarURL != "https://cdn.example/lan.png" || renamed.Ver != 2 ||
		!renamed.UpdatedAt.Equal(renameAt) || renamed.UpdatedBy != "cli:ops" {
		t.Fatalf("renamed user = %+v; want %q, avatar kept, ver 2, updated at %v", renamed, name, renameAt)
	}
	avatar := "https://cdn.example/lan.png"
	same := store.ProfileUpdate{DisplayName: &name, AvatarURL: &avatar}
	for _, u := range []store.ProfileUpdate{same, {}} {
		if err := s.SetProfile(t.Context(), "shop", "lan", u, renameAt.Add(time.Hour), "cli:other"); err != nil {
			t.Fatalf("SetProfile with the same values: %v", err)
		}
		if again := readUserDoc(t, s, "shop", "lan"); again.Ver != 2 || !again.UpdatedAt.Equal(renameAt) || again.UpdatedBy != "cli:ops" {
			t.Fatalf("user after the same values = %+v; want ver 2 updated at %v by cli:ops", again, renameAt)
		}
	}
	if err := s.SetProfile(t.Context(), "shop", "ghost", same, renameAt, "cli:ops"); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("SetProfile of an unknown user = %v; want not found", err)
	}
}

func TestSetProfileAndStateStoreDollarValuesLiterally(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "")
	name, avatar := "$avatar_url", "$$NOW"
	if err := s.SetProfile(t.Context(), "shop", "lan", store.ProfileUpdate{DisplayName: &name, AvatarURL: &avatar}, itAt, "$updated_by"); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	if err := s.SetUserState(t.Context(), "shop", "lan", store.StateDisabled, itAt, "$ver"); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}
	got := readUserDoc(t, s, "shop", "lan")
	if got.DisplayName != name || got.AvatarURL != avatar || got.UpdatedBy != "$ver" || got.Ver != 3 {
		t.Fatalf("user = %+v; want the dollar values stored as text and ver 3", got)
	}
}
