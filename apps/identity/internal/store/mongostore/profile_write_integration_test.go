package mongostore

import (
	"context"
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

func mustCreateUser(t *testing.T, s *Store, tenantID, id, name, avatar string) {
	t.Helper()
	w := store.ProfileWrite{TenantID: tenantID, UserID: id, DisplayName: name, AvatarURL: avatar, At: itAt, By: jitBy}
	if !mustWriteProfile(t, s, w) {
		t.Fatalf("create user %s/%s: not written", tenantID, id)
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

func TestWriteProfileCreatesAUserOnce(t *testing.T) {
	s := testDatabase(t)
	if !mustWriteProfile(t, s, jitWrite("lan", "Lan", "https://cdn.example/lan.png", nil, itAt)) {
		t.Fatal("first create not written")
	}
	got := readUserDoc(t, s, "shop", "lan")
	if got.ID != "shop│lan" || got.TenantID != "shop" || got.UserID != "lan" || got.DisplayName != "Lan" ||
		got.AvatarURL != "https://cdn.example/lan.png" || got.State != 1 || got.Ver != 1 ||
		!got.CreatedAt.Equal(itAt) || !got.UpdatedAt.Equal(itAt) || got.UpdatedBy != jitBy {
		t.Fatalf("created user = %+v", got)
	}
	if got.Groups == nil || len(got.Groups) != 0 {
		t.Fatalf("groups = %#v; want an empty array", got.Groups)
	}
	if mustWriteProfile(t, s, jitWrite("lan", "Other", "", nil, itAt.Add(time.Minute))) {
		t.Fatal("second create written; want false")
	}
	if again := readUserDoc(t, s, "shop", "lan"); again.DisplayName != "Lan" || again.Ver != 1 || !again.UpdatedAt.Equal(itAt) {
		t.Fatalf("user after the second create = %+v; want it unchanged", again)
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
}

func TestWriteProfileRacingCreatesWriteExactlyOnce(t *testing.T) {
	s := testDatabase(t)
	start := make(chan struct{})
	type result struct {
		written bool
		err     error
	}
	results := make(chan result, 2)
	for _, name := range []string{"Lan A", "Lan B"} {
		go func() {
			<-start
			written, err := s.WriteProfile(context.Background(), jitWrite("lan", name, "", nil, itAt))
			results <- result{written, err}
		}()
	}
	close(start)
	wins := 0
	for range 2 {
		r := waitFor(t, results, "racing create")
		if r.err != nil {
			t.Fatalf("racing WriteProfile: %v", r.err)
		}
		if r.written {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("written creates = %d; want exactly 1", wins)
	}
	if n := countUsers(t, s); n != 1 {
		t.Fatalf("users = %d; want 1", n)
	}
}
