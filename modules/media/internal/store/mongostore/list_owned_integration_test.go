package mongostore

import (
	"fmt"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

func winningStages(t *testing.T, db *mongo.Database, cmd bson.D) (stages, indexes []string) {
	t.Helper()
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: cmd}, {Key: "verbosity", Value: "queryPlanner"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	winning, err := raw.LookupErr("queryPlanner", "winningPlan")
	if err != nil {
		t.Fatalf("no winning plan in %s", raw)
	}
	var walk func(v bson.RawValue)
	walk = func(v bson.RawValue) {
		if arr, ok := v.ArrayOK(); ok {
			values, _ := arr.Values()
			for _, x := range values {
				walk(x)
			}
			return
		}
		d, ok := v.DocumentOK()
		if !ok {
			return
		}
		if stage, ok := d.Lookup("stage").StringValueOK(); ok {
			stages = append(stages, stage)
		}
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			indexes = append(indexes, name)
		}
		elems, _ := d.Elements()
		for _, e := range elems {
			walk(e.Value())
		}
	}
	walk(winning)
	return stages, indexes
}

func TestListOwnedUsesTheOwnerIndex(t *testing.T) {
	db := itDatabase(t)
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s := NewUploads(db)
	for i := range 20 {
		for _, user := range []string{"an", "an2", "an-b"} {
			if err := s.Insert(t.Context(), storetest.Uploaded("acme", user, fmt.Sprintf("%04d", i), "image/png")); err != nil {
				t.Fatal(err)
			}
		}
	}
	index, err := mongoindex.Name(mongo.IndexModel{Keys: uploadsByOwner})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bson.D{
		"first page":            ownedFilter("acme", "an", "", domain.Kinds{}),
		"below the public keys": ownedFilter("acme", "an", domain.PublicKeyPrefix("acme", "an"), domain.Kinds{}),
		"after a cursor":        ownedFilter("acme", "an", "acme/an/20261107/0010", domain.Kinds{}),
		"images and videos":     ownedFilter("acme", "an", "", domain.Kinds{Image: true, Video: true}),
		"other with a cursor":   ownedFilter("acme", "an", "acme/an/20261107/0010", domain.Kinds{Other: true}),
		"other and the images":  ownedFilter("acme", "an", "", domain.Kinds{Image: true, Other: true}),
	}
	for name, filter := range cases {
		find := bson.D{
			{Key: "find", Value: uploadsCollection}, {Key: "filter", Value: filter},
			{Key: "sort", Value: bson.D{{Key: "_id", Value: -1}}}, {Key: "limit", Value: 5}, {Key: "hint", Value: uploadsByOwner},
		}
		stages, indexes := winningStages(t, db, find)
		if !slices.Contains(stages, "IXSCAN") || !slices.Contains(indexes, index) || slices.Contains(stages, "COLLSCAN") || slices.Contains(stages, "SORT") {
			t.Fatalf("%s: stages %v on %v, want IXSCAN on %s without COLLSCAN or SORT", name, stages, indexes, index)
		}
	}
	got, err := s.ListOwned(t.Context(), "acme", "an", "", domain.Kinds{}, 100)
	if err != nil || len(got) != 20 {
		t.Fatalf("listed %d, %v", len(got), err)
	}
}
