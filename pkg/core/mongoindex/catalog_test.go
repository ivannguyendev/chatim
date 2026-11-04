package mongoindex

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestNameMatchesTheDriverGeneratedName(t *testing.T) {
	cases := []struct {
		model mongo.IndexModel
		want  string
	}{
		{mongo.IndexModel{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "created_at", Value: -1}}}, "room_id_1_created_at_-1"},
		{mongo.IndexModel{Keys: bson.D{{Key: "seq", Value: int64(1)}, {Key: "text", Value: "text"}}}, "seq_1_text_text"},
		{mongo.IndexModel{Keys: bson.D{{Key: "a", Value: 1}}, Options: options.Index().SetUnique(true)}, "a_1"},
		{mongo.IndexModel{Keys: bson.D{{Key: "a", Value: 1}}, Options: options.Index().SetName("state_pending")}, "state_pending"},
	}
	for _, c := range cases {
		if got, err := Name(c.model); err != nil || got != c.want {
			t.Errorf("Name(%v) = %q, %v; want %q", c.model.Keys, got, err, c.want)
		}
	}
}

func TestPatternKeepsFieldOrderAndDirection(t *testing.T) {
	got, err := Pattern(bson.D{{Key: "b", Value: -1}, {Key: "a", Value: 1.0}})
	if err != nil || got != "b:-1,a:1" {
		t.Fatalf("Pattern = %q, %v; want b:-1,a:1", got, err)
	}
	raw, _ := bson.Marshal(bson.D{{Key: "b", Value: int64(-1)}, {Key: "a", Value: int32(1)}})
	if same, err := Pattern(bson.Raw(raw)); err != nil || same != got {
		t.Fatalf("Pattern(raw) = %q, %v; want %q", same, err, got)
	}
	if _, err := Pattern(bson.D{{Key: "a", Value: true}}); err == nil {
		t.Fatal("Pattern(bool direction) = nil error, want an error")
	}
}

func TestCatalogWithJoinsWithoutTouchingTheParts(t *testing.T) {
	core := Catalog{Collections: []Collection{{Name: "members"}}, Replacements: []Replacement{{Collection: "members", Old: "x_1", New: "x_1_y_1"}}}
	app := Catalog{Collections: []Collection{{Name: "mentions"}}, Replacements: []Replacement{{Collection: "mentions", Old: "z_1"}}}
	all := core.With(app)
	if got := all.Names(); !slices.Equal(got, []string{"members", "mentions"}) {
		t.Fatalf("Names = %v", got)
	}
	if len(all.Replacements) != 2 || len(core.Replacements) != 1 || len(core.Collections) != 1 {
		t.Fatalf("With = %+v, core = %+v; want joined copy and core unchanged", all, core)
	}
	if got := all.replacementsOf("mentions"); len(got) != 1 || !got[0].Retired() {
		t.Fatalf("replacementsOf(mentions) = %+v, want one retired entry", got)
	}
}
