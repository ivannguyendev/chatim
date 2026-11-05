package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

func TestIndexesDeclareTheFourSecondaryIndexes(t *testing.T) {
	got := map[string]string{}
	for _, c := range Indexes().Collections {
		for _, m := range c.Indexes {
			name, err := mongoindex.Name(m)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			got[name] = c.Name
			if m.Options == nil {
				continue
			}
			var resolved options.IndexOptions
			for _, set := range m.Options.List() {
				if err := set(&resolved); err != nil {
					t.Fatalf("options of %s: %v", name, err)
				}
			}
			if resolved.Unique != nil && *resolved.Unique {
				t.Fatalf("index %s on %s is unique; want no unique index besides _id", name, c.Name)
			}
		}
	}
	want := map[string]string{
		"tenant_id_1_groups_1":            "users",
		"tenant_id_1_ancestors_1":         "groups",
		"tenant_id_1_parent_id_1_state_1": "groups",
		"expires_at_1":                    "revoked_sessions",
	}
	if len(got) != len(want) {
		t.Fatalf("indexes = %v; want %v", got, want)
	}
	for name, coll := range want {
		if got[name] != coll {
			t.Fatalf("index %s on %q; want %q (all %v)", name, got[name], coll, got)
		}
	}
}

func TestIndexesListTheNineCollections(t *testing.T) {
	want := []string{
		"tenants", "users", "groups", "trusted_issuers", "trusted_keys",
		"signing_keys", "signing_key_claims", "revoked_sessions", "slots",
	}
	if got := Indexes().Names(); !slices.Equal(got, want) {
		t.Fatalf("collections = %v; want %v", got, want)
	}
}
