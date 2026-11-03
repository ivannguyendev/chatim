package mongostore

import (
	"errors"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func insertUsers(t *testing.T, s *Store, tenantID string, n int) {
	t.Helper()
	docs := make([]any, 0, n)
	for i := range n {
		id := fmt.Sprintf("u%05d", i)
		docs = append(docs, bson.D{
			{Key: "_id", Value: tenantKey(tenantID, id)}, {Key: "tenant_id", Value: tenantID}, {Key: "id", Value: id},
			{Key: "display_name", Value: "User " + id}, {Key: "avatar_url", Value: ""}, {Key: "state", Value: store.StateActive},
			{Key: "groups", Value: bson.A{}}, {Key: "ver", Value: int64(i%3 + 1)}, {Key: "created_at", Value: itAt},
			{Key: "updated_at", Value: itAt}, {Key: "updated_by", Value: jitBy},
		})
	}
	if _, err := s.db.Collection(usersCollection).InsertMany(t.Context(), docs); err != nil {
		t.Fatalf("insert %d users of %s: %v", n, tenantID, err)
	}
}

func TestLoadPageReadsEveryUserOnceInIDOrder(t *testing.T) {
	s := testDatabase(t)
	insertUsers(t, s, "shop", 7000)
	insertUsers(t, s, "mall", 5345)
	var all []store.DirectoryEntry
	var after store.UserKey
	pages := 0
	for {
		page, err := s.LoadPage(t.Context(), after, 5000)
		if err != nil {
			t.Fatalf("LoadPage after %+v: %v", after, err)
		}
		if len(page) == 0 {
			break
		}
		pages++
		all = append(all, page...)
		last := page[len(page)-1]
		after = store.UserKey{TenantID: last.TenantID, ID: last.ID}
	}
	if len(all) != 12345 || pages != 3 {
		t.Fatalf("loaded %d users in %d pages; want 12345 in 3", len(all), pages)
	}
	seen := make(map[string]bool, len(all))
	for i, e := range all {
		key := tenantKey(e.TenantID, e.ID)
		if seen[key] {
			t.Fatalf("user %s loaded twice", key)
		}
		seen[key] = true
		if i > 0 && tenantKey(all[i-1].TenantID, all[i-1].ID) >= key {
			t.Fatalf("user %s after %s; want _id order", key, tenantKey(all[i-1].TenantID, all[i-1].ID))
		}
	}
	first := all[0]
	want := store.DirectoryEntry{TenantID: "mall", ID: "u00000", DisplayName: "User u00000", State: store.StateActive, Ver: 1}
	if first != want {
		t.Fatalf("first entry = %+v; want %+v", first, want)
	}
}

func TestLoadPageRefusesANonPositiveLimit(t *testing.T) {
	s := testDatabase(t)
	for _, limit := range []int{0, -1} {
		if _, err := s.LoadPage(t.Context(), store.UserKey{}, limit); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("LoadPage limit %d = %v; want invalid argument", limit, err)
		}
	}
}
