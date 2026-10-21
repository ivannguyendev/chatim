package mongostore

import (
	"errors"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func seedIndexConflicts(t *testing.T, db *mongo.Database) {
	t.Helper()
	if err := ensureClustered(t.Context(), db, messagesCollection); err != nil {
		t.Fatalf("create messages: %v", err)
	}
	plainPending := mongo.IndexModel{Keys: bson.D{{Key: stateAtField, Value: 1}, {Key: "_id", Value: 1}}}
	if _, err := db.Collection(messagesCollection).Indexes().CreateOne(t.Context(), plainPending); err != nil {
		t.Fatalf("create a non-partial pending index: %v", err)
	}
	if err := db.CreateCollection(t.Context(), hiddenCollection); err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	notUnique := mongo.IndexModel{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "room_id", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}}
	if _, err := db.Collection(hiddenCollection).Indexes().CreateOne(t.Context(), notUnique); err != nil {
		t.Fatalf("create a non-unique hidden index: %v", err)
	}
}

func TestBootstrapFlagsOptionConflictsAndTheCommandRefusesThem(t *testing.T) {
	db := itDatabase(t, itClient(t))
	seedIndexConflicts(t, db)
	health := mongoindex.NewHealth(slog.New(slog.DiscardHandler))
	if err := Bootstrap(t.Context(), db, health); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !health.Outdated(messagesCollection) || !health.Outdated(hiddenCollection) || health.Outdated(roomsCollection) {
		t.Fatalf("outdated messages/hidden/rooms = %v/%v/%v, want true/true/false", health.Outdated(messagesCollection), health.Outdated(hiddenCollection), health.Outdated(roomsCollection))
	}
	if got := indexKeys(t, db.Collection(hiddenCollection)); !hasIndex(got, "user_id:1,room_id:1,thread_root:1,seq:1", false) {
		t.Fatalf("hidden indexes = %v, want the non-unique index left alone", got)
	}
	if _, err := db.Collection(membersCollection).InsertOne(t.Context(), bson.D{{Key: "_id", Value: []byte("seed")}}); err != nil {
		t.Fatalf("seed a member: %v", err)
	}
	if err := db.Collection(membersCollection).Indexes().DropOne(t.Context(), membersUserIndex); err != nil {
		t.Fatalf("drop the members user index: %v", err)
	}
	reports, err := mongoindex.Inspect(t.Context(), db, Indexes())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var lines []string
	for _, r := range reports {
		lines = append(lines, r.Lines()...)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"messages conflict _state_at_1__id_1: declared state_pending", "hidden conflict user_id_1_room_id_1_thread_root_1_seq_1: declared user_id_1_room_id_1_thread_root_1_seq_1 with options [unique], found []", "members missing " + membersUserIndex} {
		if !strings.Contains(all, want) {
			t.Errorf("dry run lines %q lack %q", all, want)
		}
	}
	before := allIndexes(t, db)
	if created, err := mongoindex.Apply(t.Context(), db, Indexes()); !errors.Is(err, mongoindex.ErrIndexConflict) || len(created) != 0 {
		t.Fatalf("Apply = %v, %v; want ErrIndexConflict and nothing created", created, err)
	}
	if dropped, err := mongoindex.DropOld(t.Context(), db, Indexes()); !errors.Is(err, mongoindex.ErrIndexConflict) || len(dropped) != 0 {
		t.Fatalf("DropOld = %v, %v; want ErrIndexConflict and nothing dropped", dropped, err)
	}
	if after := allIndexes(t, db); !maps.EqualFunc(before, after, maps.Equal) {
		t.Fatalf("indexes after refused commands = %v, want unchanged %v", after, before)
	}
}
