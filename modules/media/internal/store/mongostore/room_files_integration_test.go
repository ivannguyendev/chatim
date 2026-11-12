package mongostore

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

func examined(t *testing.T, db *mongo.Database, cmd bson.D) (docs, keys int64) {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	stats, err := raw.LookupErr("executionStats")
	if err != nil {
		t.Fatalf("no execution stats in %s", raw)
	}
	doc := stats.Document()
	return doc.Lookup("totalDocsExamined").AsInt64(), doc.Lookup("totalKeysExamined").AsInt64()
}

func seedRoomPairs(t *testing.T, db *mongo.Database) []domain.MessageFile {
	t.Helper()
	base := time.UnixMilli(1_790_000_000_000).UTC()
	var pairs, roomA []domain.MessageFile
	for m := range uint64(50) {
		at := base.Add(time.Duration(m) * time.Minute)
		for i, room := range []string{"8812", "9001"} {
			p := storetest.RoomPair("acme", room, m+1, fmt.Sprintf("acme/an/20261111/%04d-%d", m, i), domain.RoomFileMedia, at)
			pairs = append(pairs, p)
			if room == "8812" {
				roomA = append(roomA, p)
			}
		}
		pairs = append(pairs, storetest.RoomPair("acme", "8812", m+1, fmt.Sprintf("acme/an/20261111/%04d-pdf", m), domain.RoomFileOther, at))
	}
	if err := NewMessageFiles(db).Attach(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	return roomA
}

func TestListRoomUsesTheRoomIndexWithoutReadingDocuments(t *testing.T) {
	db := itBootstrapped(t, itDatabase(t))
	roomA := seedRoomPairs(t, db)
	index, err := mongoindex.Name(mongo.IndexModel{Keys: filesByRoom})
	if err != nil {
		t.Fatal(err)
	}
	first := domain.RoomFilesQuery{Tenant: "acme", Room: "8812", Kind: domain.RoomFileMedia, Limit: 10}
	later := first
	later.Before, later.ClearedAt = domain.CursorOf(roomA[30]), roomA[5].SentAt
	cases := []struct {
		name     string
		q        domain.RoomFilesQuery
		maxKeys  int64
		wantSeqs int
	}{
		{"first page", first, 11, 10},
		{"a cursor after a history clear", later, 22, 10},
	}
	for _, c := range cases {
		find := bson.D{
			{Key: "find", Value: messageFilesCollection}, {Key: "filter", Value: roomFilter(c.q)},
			{Key: "sort", Value: roomSort}, {Key: "projection", Value: roomProjection},
			{Key: "limit", Value: c.q.Limit}, {Key: "hint", Value: filesByRoom},
		}
		stages, indexes := winningStages(t, db, find)
		t.Logf("%s: stages %v on %v", c.name, stages, indexes)
		if !slices.Contains(stages, "IXSCAN") || !slices.Contains(indexes, index) || slices.Contains(stages, "COLLSCAN") || slices.Contains(stages, "SORT") || slices.Contains(stages, "FETCH") {
			t.Fatalf("%s: stages %v on %v, want IXSCAN on %s without COLLSCAN, SORT or FETCH", c.name, stages, indexes, index)
		}
		docs, keys := examined(t, db, find)
		if docs != 0 || keys > c.maxKeys {
			t.Fatalf("%s: examined %d docs and %d keys, want 0 docs and at most %d keys", c.name, docs, keys, c.maxKeys)
		}
		got, err := NewMessageFiles(db).ListRoom(t.Context(), c.q)
		if err != nil || len(got) != c.wantSeqs {
			t.Fatalf("%s: listed %d, %v", c.name, len(got), err)
		}
	}
}
