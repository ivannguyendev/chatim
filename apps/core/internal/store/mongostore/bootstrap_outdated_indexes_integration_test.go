package mongostore

import (
	"bytes"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

var oldMembersUserKeys = bson.D{
	{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1},
	{Key: "joined_at", Value: 1}, {Key: "cleared_at", Value: 1}, {Key: "read_seq", Value: 1},
	{Key: "pinned_at", Value: 1}, {Key: "archived_at", Value: 1},
}

func allIndexes(t *testing.T, db *mongo.Database) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	for _, name := range Indexes().Names() {
		out[name] = indexKeys(t, db.Collection(name))
	}
	return out
}

func TestBootstrapOfAFreshDatabaseCreatesEveryDeclaredIndex(t *testing.T) {
	db := itDatabase(t, itClient(t))
	var logs bytes.Buffer
	health := mongoindex.NewHealth(slog.New(slog.NewTextHandler(&logs, nil)))
	if err := Bootstrap(t.Context(), db, health); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	reports, err := mongoindex.Inspect(t.Context(), db, Indexes())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	for _, r := range reports {
		if r.Stale() || r.Absent || len(r.Unknown) > 0 || health.Outdated(r.Collection) {
			t.Errorf("%s report = %+v, outdated = %v; want every declared index and nothing else", r.Collection, r, health.Outdated(r.Collection))
		}
	}
	if logs.Len() != 0 {
		t.Fatalf("logs = %q, want no warning", logs.String())
	}
	assertMemberIndexes(t, db)
}

func TestBootstrapLeavesACollectionWithDataUnchangedAndFlagsIt(t *testing.T) {
	s, db := itStore(t, itClient(t))
	itRoomWithOwner(t, s)
	members := db.Collection(membersCollection).Indexes()
	if err := members.DropOne(t.Context(), membersUserIndex); err != nil {
		t.Fatalf("drop the new members index: %v", err)
	}
	if _, err := members.CreateOne(t.Context(), mongo.IndexModel{Keys: oldMembersUserKeys}); err != nil {
		t.Fatalf("create the old members index: %v", err)
	}
	old := mongo.IndexModel{Keys: bson.D{{Key: "message_key", Value: 1}, {Key: "kind", Value: 1}, {Key: "state", Value: 1}, {Key: "value", Value: 1}}}
	if _, err := db.Collection(interactionsCollection).Indexes().CreateOne(t.Context(), old); err != nil {
		t.Fatalf("create the old interactions index: %v", err)
	}
	before := allIndexes(t, db)
	var logs bytes.Buffer
	health := mongoindex.NewHealth(slog.New(slog.NewTextHandler(&logs, nil)))
	for range 2 {
		if err := Bootstrap(t.Context(), db, health); err != nil {
			t.Fatalf("Bootstrap: %v", err)
		}
	}
	if after := allIndexes(t, db); !maps.EqualFunc(before, after, maps.Equal) {
		t.Fatalf("indexes after bootstrap = %v, want unchanged %v", after, before)
	}
	if !health.Outdated(membersCollection) || !health.Outdated(interactionsCollection) || health.Outdated(roomsCollection) {
		t.Fatalf("outdated members/interactions/rooms = %v/%v/%v, want true/true/false", health.Outdated(membersCollection), health.Outdated(interactionsCollection), health.Outdated(roomsCollection))
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, mongoindex.OutdatedWarning) || !strings.Contains(out, membersUserIndex) || !strings.Contains(out, "message_key_1_kind_1_state_1_value_1") {
		t.Fatalf("logs = %q, want a warning naming the missing and outdated indexes", out)
	}
}

func TestBootstrapCreatesAMissingIndexOnAnEmptyCollection(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if err := db.Collection(membersCollection).Indexes().DropOne(t.Context(), membersUserIndex); err != nil {
		t.Fatalf("drop the members index: %v", err)
	}
	health := mongoindex.NewHealth(slog.New(slog.DiscardHandler))
	if err := Bootstrap(t.Context(), db, health); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	assertMemberIndexes(t, db)
	if health.Outdated(membersCollection) {
		t.Fatal("empty members flagged outdated after bootstrap built its index")
	}
}
