package mongostore

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var identityCollections = []string{
	"tenants", "users", "groups", "trusted_issuers", "trusted_keys",
	"signing_keys", "signing_key_claims", "revoked_sessions", "slots",
}

var identityIndexLines = []string{
	"users missing tenant_id_1_groups_1",
	"groups missing tenant_id_1_ancestors_1",
	"groups missing tenant_id_1_parent_id_1_state_1",
	"revoked_sessions missing expires_at_1",
}

func TestMigrateDryRunListsEverythingAndWritesNothing(t *testing.T) {
	s := emptyDatabase(t)
	lines, err := s.Migrate(t.Context(), false)
	if err != nil {
		t.Fatalf("Migrate dry-run: %v", err)
	}
	var want []string
	for _, name := range identityCollections {
		want = append(want, "collection "+name+" absent")
	}
	want = append(want, identityIndexLines...)
	if !sameLines(lines, want) {
		t.Fatalf("dry-run lines = %q; want %q", lines, want)
	}
	if names := collectionNames(t, s); len(names) != 0 {
		t.Fatalf("collections after dry-run = %v; want none", names)
	}
}

func TestMigrateApplyBuildsClusteredCollectionsAndIndexesOnce(t *testing.T) {
	s := emptyDatabase(t)
	if _, err := s.Migrate(t.Context(), true); err != nil {
		t.Fatalf("Migrate apply: %v", err)
	}
	names := collectionNames(t, s)
	if !sameLines(names, identityCollections) {
		t.Fatalf("collections = %v; want %v", names, identityCollections)
	}
	specs, err := s.db.ListCollectionSpecifications(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	for _, spec := range specs {
		if dir, ok := spec.Options.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || dir != 1 {
			t.Fatalf("collection %s options %s; want clusteredIndex.key._id = 1", spec.Name, spec.Options)
		}
	}
	for coll, want := range map[string][]string{
		"users":            {"tenant_id_1_groups_1"},
		"groups":           {"tenant_id_1_ancestors_1", "tenant_id_1_parent_id_1_state_1"},
		"revoked_sessions": {"expires_at_1"},
	} {
		if got := secondaryIndexes(t, s, coll); !sameLines(got, want) {
			t.Fatalf("indexes of %s = %v; want %v", coll, got, want)
		}
	}
	again, err := s.Migrate(t.Context(), true)
	if err != nil || len(again) != 0 {
		t.Fatalf("Migrate again = %q, %v; want no lines", again, err)
	}
	dry, err := s.Migrate(t.Context(), false)
	if err != nil || len(dry) != 0 {
		t.Fatalf("dry-run when complete = %q, %v; want no lines", dry, err)
	}
	state, err := s.Schema(t.Context())
	if err != nil || len(state.Missing) != 0 || len(state.Outdated) != 0 {
		t.Fatalf("Schema = %+v, %v; want complete", state, err)
	}
}

func TestSchemaReportsADroppedIndex(t *testing.T) {
	s := testDatabase(t)
	if err := s.db.Collection("users").Indexes().DropOne(t.Context(), "tenant_id_1_groups_1"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	state, err := s.Schema(t.Context())
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if want := []string{"index users tenant_id_1_groups_1 missing"}; !slices.Equal(state.Missing, want) || len(state.Outdated) != 0 {
		t.Fatalf("Schema = %+v; want Missing %q only", state, want)
	}
}

func TestSchemaAndMigrateRefuseACollectionNotClustered(t *testing.T) {
	s := emptyDatabase(t)
	if err := s.db.CreateCollection(t.Context(), "slots"); err != nil {
		t.Fatalf("create plain slots: %v", err)
	}
	state, err := s.Schema(t.Context())
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if !slices.Contains(state.Missing, "collection slots not clustered on _id") {
		t.Fatalf("Missing = %q; want slots not clustered", state.Missing)
	}
	lines, err := s.Migrate(t.Context(), false)
	if err != nil || !slices.Contains(lines, "collection slots not clustered on _id, recreate it") {
		t.Fatalf("dry-run = %q, %v; want the recreate line", lines, err)
	}
	if _, err := s.Migrate(t.Context(), true); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("Migrate apply = %v; want failed precondition", err)
	}
	if names := collectionNames(t, s); !slices.Equal(names, []string{"slots"}) {
		t.Fatalf("collections after refused apply = %v; want only slots", names)
	}
}

func TestSchemaReportsAnIndexWithOtherOptionsAsOutdated(t *testing.T) {
	s := testDatabase(t)
	sessions := s.db.Collection("revoked_sessions").Indexes()
	if err := sessions.DropOne(t.Context(), "expires_at_1"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := sessions.CreateOne(t.Context(), mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetSparse(true),
	}); err != nil {
		t.Fatalf("create sparse index: %v", err)
	}
	state, err := s.Schema(t.Context())
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if !slices.Equal(state.Outdated, []string{"revoked_sessions"}) || len(state.Missing) != 0 {
		t.Fatalf("Schema = %+v; want Outdated [revoked_sessions] only", state)
	}
	lines, err := s.Migrate(t.Context(), false)
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "revoked_sessions conflict expires_at_1") {
		t.Fatalf("dry-run = %q, %v; want one conflict line", lines, err)
	}
	if _, err := s.Migrate(t.Context(), true); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("Migrate apply = %v; want failed precondition", err)
	}
}

func collectionNames(t *testing.T, s *Store) []string {
	t.Helper()
	names, err := s.db.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	return names
}

func secondaryIndexes(t *testing.T, s *Store, coll string) []string {
	t.Helper()
	specs, err := s.db.Collection(coll).Indexes().ListSpecifications(t.Context())
	if err != nil {
		t.Fatalf("list indexes of %s: %v", coll, err)
	}
	var out []string
	for _, spec := range specs {
		if spec.Name != "_id_" {
			out = append(out, spec.Name)
		}
	}
	return out
}

func sameLines(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}
