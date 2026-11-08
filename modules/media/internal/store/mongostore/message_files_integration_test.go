package mongostore

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

func itBootstrapped(t *testing.T, db *mongo.Database) *mongo.Database {
	t.Helper()
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMessageFilesContractOnMongo(t *testing.T) {
	storetest.RunMessageFiles(t, func(t *testing.T) storetest.Stores {
		db := itBootstrapped(t, itDatabase(t))
		return storetest.Stores{Files: NewMessageFiles(db), Uploads: NewUploads(db)}
	})
}

func TestReleaseKeepsTheFirstDeletedAt(t *testing.T) {
	db := itBootstrapped(t, itDatabase(t))
	s := NewMessageFiles(db)
	pair := storetest.Pair("acme", "8812-0-42", "acme/an/20261108/xxxx")
	first := time.UnixMilli(1_790_000_100_000).UTC()
	if err := s.Attach(t.Context(), []domain.MessageFile{pair}); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{first, first.Add(time.Hour)} {
		if err := s.Release(t.Context(), "acme", pair.MessageKey, at); err != nil {
			t.Fatal(err)
		}
	}
	var doc struct {
		State     int32     `bson:"state"`
		DeletedAt time.Time `bson:"deleted_at"`
		Tenant    string    `bson:"tenant_id"`
	}
	if err := db.Collection(messageFilesCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: pairID(pair.MessageKey, pair.FileKey)}}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.State != int32(domain.PairDeleted) || !doc.DeletedAt.Equal(first) || doc.Tenant != "acme" {
		t.Fatalf("pair = %+v", doc)
	}
}

func seedPairs(t *testing.T, db *mongo.Database) {
	t.Helper()
	s := NewMessageFiles(db)
	var pairs []domain.MessageFile
	for m := range 30 {
		for f := range 3 {
			for _, tenant := range []string{"acme", "other"} {
				pairs = append(pairs, storetest.Pair(tenant, fmt.Sprintf("88%02d-0-%d", m, f), fmt.Sprintf("%s/an/20261108/%04d", tenant, m*3+f)))
			}
		}
	}
	if err := s.Attach(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(t.Context(), "acme", "8800-0-0", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func assertIndexScan(t *testing.T, name string, stages, indexes []string, index string) {
	t.Helper()
	t.Logf("%s: stages %v on %v", name, stages, indexes)
	if !slices.Contains(stages, "IXSCAN") || slices.Contains(stages, "COLLSCAN") || !slices.Contains(indexes, index) {
		t.Fatalf("%s: stages %v on %v, want IXSCAN on %s without COLLSCAN", name, stages, indexes, index)
	}
}

func assertClusteredScan(t *testing.T, name string, stages, indexes []string) {
	t.Helper()
	t.Logf("%s: stages %v on %v", name, stages, indexes)
	clustered := slices.ContainsFunc(stages, func(s string) bool {
		return s == "CLUSTERED_IXSCAN" || s == "EXPRESS_CLUSTERED_IXSCAN" || s == "IDHACK"
	})
	if !clustered || slices.Contains(stages, "COLLSCAN") || len(indexes) > 0 {
		t.Fatalf("%s: stages %v on %v, want a bounded _id scan", name, stages, indexes)
	}
}

func TestMessageFileQueriesUseTheirIndexes(t *testing.T) {
	db := itBootstrapped(t, itDatabase(t))
	seedPairs(t, db)
	byMessage, err := mongoindex.Name(mongo.IndexModel{Keys: filesByMessage})
	if err != nil {
		t.Fatal(err)
	}
	byKey, err := mongoindex.Name(mongo.IndexModel{Keys: filesByKey})
	if err != nil {
		t.Fatal(err)
	}
	active := bson.D{
		{Key: "find", Value: messageFilesCollection}, {Key: "filter", Value: activeFilter("acme", "8805-0-1")},
		{Key: "projection", Value: bson.D{{Key: "_id", Value: 0}, {Key: "file_key", Value: 1}}}, {Key: "hint", Value: filesByMessage},
	}
	stages, indexes := winningStages(t, db, active)
	assertIndexScan(t, "Active", stages, indexes, byMessage)
	release := bson.D{{Key: "update", Value: messageFilesCollection}, {Key: "updates", Value: bson.A{bson.D{
		{Key: "q", Value: activeFilter("acme", "8805-0-1")},
		{Key: "u", Value: bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: int32(domain.PairDeleted)}}}}},
		{Key: "multi", Value: true}, {Key: "hint", Value: filesByMessage},
	}}}}
	stages, indexes = winningStages(t, db, release)
	assertIndexScan(t, "Release", stages, indexes, byMessage)
	keys := []string{"acme/an/20261108/0001", "acme/an/20261108/0016", "acme/an/20261108/9999"}
	distinct := bson.D{{Key: "distinct", Value: messageFilesCollection}, {Key: "key", Value: "file_key"}, {Key: "query", Value: stillUsedFilter(keys, "8805-0-1")}}
	stages, indexes = winningStages(t, db, distinct)
	t.Logf("StillUsed: stages %v on %v", stages, indexes)
	if !slices.Contains(stages, "DISTINCT_SCAN") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") || !slices.Contains(indexes, byKey) {
		t.Fatalf("StillUsed: stages %v on %v, want DISTINCT_SCAN on %s without FETCH or COLLSCAN", stages, indexes, byKey)
	}
}

func TestUploadLocationQueriesScanByID(t *testing.T) {
	db := itBootstrapped(t, itDatabase(t))
	s := NewUploads(db)
	var keys []string
	for i := range 40 {
		u := storetest.Uploaded("acme", "an", fmt.Sprintf("%04d", i), "image/png")
		if err := s.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, u.Key)
	}
	for name, filter := range map[string]bson.D{
		"GetMany one key":   manyFilter("acme", keys[:1]),
		"GetMany many keys": manyFilter("acme", keys[3:13]),
		"Retire":            retireFilter(keys[3:13]),
	} {
		stages, indexes := winningStages(t, db, bson.D{{Key: "find", Value: uploadsCollection}, {Key: "filter", Value: filter}})
		assertClusteredScan(t, name, stages, indexes)
	}
}

func TestGetManyReadsInBatchesOf200(t *testing.T) {
	var finds atomic.Int64
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" {
			finds.Add(1)
		}
	}}
	db := itBootstrapped(t, itDatabaseWatched(t, monitor))
	s := NewUploads(db)
	var keys []string
	for i := range 401 {
		u := storetest.Uploaded("acme", "an", fmt.Sprintf("%04d", i), "image/png")
		if err := s.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, u.Key)
	}
	finds.Store(0)
	got, err := s.GetMany(t.Context(), "acme", append(keys, keys[:50]...))
	if err != nil || len(got) != len(keys) {
		t.Fatalf("got %d uploads, %v", len(got), err)
	}
	if n := finds.Load(); n != 3 {
		t.Fatalf("finds = %d, want 3 for 401 keys", n)
	}
	other, err := s.GetMany(t.Context(), "other", keys)
	if err != nil || len(other) != 0 {
		t.Fatalf("another tenant read %d uploads, %v", len(other), err)
	}
}
