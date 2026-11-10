package mongostore

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const jitBy = "jit:shop-backend"

func jitWrite(user, name, avatar string, seen *store.SeenProfile, at time.Time) store.ProfileWrite {
	return store.ProfileWrite{TenantID: "shop", UserID: user, DisplayName: name, AvatarURL: avatar, Seen: seen, At: at, By: jitBy}
}

func mustWriteProfile(t *testing.T, s *Store, w store.ProfileWrite) bool {
	t.Helper()
	written, err := s.WriteProfile(t.Context(), w)
	if err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	return written
}

func subjectUser(tenantID, id, name, avatar string) store.NewUser {
	return store.NewUser{TenantID: tenantID, Realm: itRealm, Sub: id, CandidateID: id, DisplayName: name, AvatarURL: avatar, At: itAt, By: jitBy}
}

func mustCreateUser(t *testing.T, s *Store, tenantID, id, name, avatar string) {
	t.Helper()
	if got := mustLinkUser(t, s, subjectUser(tenantID, id, name, avatar)); got != (store.LinkedUser{ID: id, Result: store.LinkCreated}) {
		t.Fatalf("create user %s/%s = %+v; want it created", tenantID, id, got)
	}
}

func countUsers(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.db.Collection(usersCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}

func TestLinkUserWritesAFullUserDoc(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "https://cdn.example/lan.png")
	got := readUserDoc(t, s, "shop", "lan")
	if got.ID != "shop│lan" || got.TenantID != "shop" || got.UserID != "lan" || got.DisplayName != "Lan" ||
		got.AvatarURL != "https://cdn.example/lan.png" || got.State != 1 || got.Ver != 1 ||
		!got.CreatedAt.Equal(itAt) || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != jitBy {
		t.Fatalf("created user = %+v", got)
	}
	if got.Groups == nil || len(got.Groups) != 0 {
		t.Fatalf("groups = %#v; want an empty array", got.Groups)
	}
}

func TestWriteProfileUpdatesOnlyWhenTheSeenValuesStillHold(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "lan", "Lan", "https://cdn.example/lan.png")
	updateAt := itAt.Add(time.Minute)
	seen := &store.SeenProfile{DisplayName: "Lan", AvatarURL: "https://cdn.example/lan.png"}
	if !mustWriteProfile(t, s, jitWrite("lan", "Lan Nguyen", "https://cdn.example/lan2.png", seen, updateAt)) {
		t.Fatal("update with the right seen values not written")
	}
	updated := readUserDoc(t, s, "shop", "lan")
	if updated.DisplayName != "Lan Nguyen" || updated.AvatarURL != "https://cdn.example/lan2.png" || updated.Ver != 2 ||
		!updated.CreatedAt.Equal(itAt) || !updated.UpdatedAt.Equal(updateAt) || updated.UpdatedBy != jitBy {
		t.Fatalf("updated user = %+v", updated)
	}
	if mustWriteProfile(t, s, jitWrite("lan", "Stale", "", seen, updateAt.Add(time.Minute))) {
		t.Fatal("update with stale seen values written; want false")
	}
	if again := readUserDoc(t, s, "shop", "lan"); again.DisplayName != "Lan Nguyen" || again.AvatarURL != "https://cdn.example/lan2.png" ||
		again.Ver != 2 || !again.UpdatedAt.Equal(updateAt) {
		t.Fatalf("user after the lost CAS = %+v; want it unchanged", again)
	}
	if mustWriteProfile(t, s, jitWrite("ghost", "Ghost", "", seen, updateAt)) {
		t.Fatal("update of an unknown user written; want false")
	}
	if n := countUsers(t, s); n != 1 {
		t.Fatalf("users = %d; want 1, an update never creates a user", n)
	}
}
