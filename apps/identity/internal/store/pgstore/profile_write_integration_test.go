package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const jitUser = "u1aaaaaaaaaaaaaaaaaaaaaaaa"

var jitAt = t0.Add(time.Hour)

func jitWrite(name, avatar string, seen *store.SeenProfile) store.ProfileWrite {
	return store.ProfileWrite{
		TenantID: "shop", UserID: jitUser, DisplayName: name, AvatarURL: avatar,
		Seen: seen, At: jitAt, By: "jit:" + shopIssuer,
	}
}

func mustWriteProfile(t *testing.T, s fixture, w store.ProfileWrite) bool {
	t.Helper()
	written, err := s.WriteProfile(context.Background(), w)
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	return written
}

func readUser(t *testing.T, s fixture, id string) store.User {
	t.Helper()
	u := store.User{TenantID: "shop", ID: id}
	err := s.raw.QueryRow(context.Background(),
		"SELECT display_name, avatar_url, state, ver, created_at, updated_at, updated_by FROM users WHERE tenant_id = 'shop' AND id = $1", id).
		Scan(&u.DisplayName, &u.AvatarURL, &u.State, &u.Ver, &u.CreatedAt, &u.UpdatedAt, &u.UpdatedBy)
	if err != nil {
		t.Fatalf("read user %s: %v", id, err)
	}
	return u
}

func directoryChangeVer(t *testing.T, s fixture, id string) int64 {
	t.Helper()
	var ver int64
	err := s.raw.QueryRow(context.Background(),
		"SELECT ver FROM directory_changes WHERE kind = 1 AND tenant_id = 'shop' AND id = $1", id).Scan(&ver)
	if err != nil {
		t.Fatalf("read directory change %s: %v", id, err)
	}
	return ver
}

func TestWriteProfileInsertsANewUserOnce(t *testing.T) {
	s := openStore(t)
	seedTenant(t, s, "shop")
	if !mustWriteProfile(t, s, jitWrite("Lan", "https://cdn.example/lan.png", nil)) {
		t.Fatal("first insert written = false; want true")
	}
	got := readUser(t, s, jitUser)
	if got.DisplayName != "Lan" || got.AvatarURL != "https://cdn.example/lan.png" || got.State != store.StateActive || got.Ver != 1 ||
		!got.CreatedAt.Equal(jitAt) || !got.UpdatedAt.Equal(jitAt) || got.UpdatedBy != "jit:"+shopIssuer {
		t.Fatalf("inserted user = %+v", got)
	}
	if ver := directoryChangeVer(t, s, jitUser); ver != 1 {
		t.Fatalf("directory change ver = %d; want 1", ver)
	}
	if mustWriteProfile(t, s, jitWrite("Other", "", nil)) {
		t.Fatal("second insert written = true; want false")
	}
	if again := readUser(t, s, jitUser); again != got {
		t.Fatalf("user after duplicate insert = %+v; want %+v", again, got)
	}
	if n := auditCount(t, s, "user_set"); n != 0 {
		t.Fatalf("audit rows from jit = %d; want 0", n)
	}
}

func TestWriteProfileUpdatesOnlyWhenTheSeenValuesStillHold(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", jitUser, "Lan")
	if !mustWriteProfile(t, s, jitWrite("Lan Nguyen", "https://cdn.example/a.png", &store.SeenProfile{DisplayName: "Lan"})) {
		t.Fatal("update with matching seen written = false; want true")
	}
	got := readUser(t, s, jitUser)
	if got.DisplayName != "Lan Nguyen" || got.AvatarURL != "https://cdn.example/a.png" || got.Ver != 2 ||
		!got.UpdatedAt.Equal(jitAt) || got.UpdatedBy != "jit:"+shopIssuer || !got.CreatedAt.Equal(t0) {
		t.Fatalf("updated user = %+v", got)
	}
	if ver := directoryChangeVer(t, s, jitUser); ver != 2 {
		t.Fatalf("directory change ver = %d; want 2", ver)
	}
	between := "Set By Admin"
	if err := s.SetProfile(ctx, "shop", jitUser, store.ProfileUpdate{DisplayName: &between}, audit("user_set")); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	stale := &store.SeenProfile{DisplayName: "Lan Nguyen", AvatarURL: "https://cdn.example/a.png"}
	if mustWriteProfile(t, s, jitWrite("Lan N", "https://cdn.example/a.png", stale)) {
		t.Fatal("update with stale seen written = true; want false")
	}
	if after := readUser(t, s, jitUser); after.DisplayName != between || after.Ver != 3 {
		t.Fatalf("user after stale write = %+v; want the admin value at ver 3", after)
	}
	if n := auditCount(t, s, "user_set"); n != 1 {
		t.Fatalf("user_set audit rows = %d; want only the admin one", n)
	}
}

func TestWriteProfileNeverChangesState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedTenant(t, s, "shop")
	seedUser(t, s, "shop", jitUser, "Lan")
	if err := s.SetUserState(ctx, "shop", jitUser, store.StateDisabled, audit("user_disable")); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}
	if !mustWriteProfile(t, s, jitWrite("Lan B", "", &store.SeenProfile{DisplayName: "Lan"})) {
		t.Fatal("update of a disabled user written = false; want true")
	}
	if got := readUser(t, s, jitUser); got.State != store.StateDisabled || got.DisplayName != "Lan B" {
		t.Fatalf("user = %+v; want disabled with the new name", got)
	}
}

func TestWriteProfileFailsUnavailableWhenPostgresIsGone(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	closed, err := pgstore.Open(ctx, s.uri, pgstore.Options{MaxConns: 1, RevocationKeep: testKeep})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	closed.Close()
	for _, w := range []store.ProfileWrite{jitWrite("Lan", "", nil), jitWrite("Lan", "", &store.SeenProfile{DisplayName: "L"})} {
		written, err := closed.WriteProfile(ctx, w)
		if written || !errors.Is(err, apperr.ErrUnavailable) {
			t.Fatalf("WriteProfile on a closed pool = %v, %v; want false, ErrUnavailable", written, err)
		}
	}
}
