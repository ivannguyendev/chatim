package mongoindex

import (
	"errors"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var membersDeclared = Collection{Name: "members", Indexes: []mongo.IndexModel{
	{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "state", Value: 1}}},
	{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "ver", Value: 1}}},
}}

var membersReplaced = []Replacement{
	{Collection: "members", Old: "user_id_1_state_1", New: "user_id_1_state_1_ver_1"},
	{Collection: "members", Old: "user_id_1", New: "user_id_1_state_1_ver_1"},
	{Collection: "members", Old: "read_1"},
}

func TestCompareFindsMissingOutdatedAndUnknownIndexes(t *testing.T) {
	existing := []Existing{
		{Name: "_id_", Keys: "_id:1"},
		{Name: "room_id_1_state_1", Keys: "room_id:1,state:1"},
		{Name: "user_id_1_state_1", Keys: "user_id:1,state:1"},
		{Name: "read_1", Keys: "read:1"},
		{Name: "extra_1", Keys: "extra:1"},
	}
	r, err := Compare(membersDeclared, membersReplaced, existing)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if got := r.MissingNames(); !slices.Equal(got, []string{"user_id_1_state_1_ver_1"}) {
		t.Fatalf("missing = %v", got)
	}
	want := []Outdated{
		{Replacement: membersReplaced[0], State: ReplacementAbsent},
		{Replacement: membersReplaced[2], State: ReplacementRetired},
	}
	if !slices.Equal(r.Outdated, want) {
		t.Fatalf("outdated = %+v, want %+v", r.Outdated, want)
	}
	if !slices.Equal(r.Unknown, []string{"extra_1"}) || !r.Stale() {
		t.Fatalf("unknown = %v, stale = %v; want [extra_1] and stale", r.Unknown, r.Stale())
	}
	lines := []string{
		"members missing user_id_1_state_1_ver_1",
		"members outdated user_id_1_state_1 replaced by user_id_1_state_1_ver_1 (absent)",
		"members retired read_1",
		"members unknown extra_1",
	}
	if got := r.Lines(); !slices.Equal(got, lines) {
		t.Fatalf("Lines = %q, want %q", got, lines)
	}
}

func TestCompareMatchesDeclaredIndexesByKeysNotName(t *testing.T) {
	existing := []Existing{
		{Name: "_id_", Keys: "_id:1"},
		{Name: "custom", Keys: "room_id:1,state:1"},
		{Name: "user_id_1_state_1_ver_1", Keys: "user_id:1,state:1,ver:1", Building: true},
		{Name: "user_id_1_state_1", Keys: "user_id:1,state:1"},
	}
	r, err := Compare(membersDeclared, membersReplaced, existing)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(r.Missing) != 0 || len(r.Unknown) != 0 {
		t.Fatalf("missing = %v, unknown = %v; want none", r.MissingNames(), r.Unknown)
	}
	if len(r.Outdated) != 1 || r.Outdated[0].State != ReplacementBuilding {
		t.Fatalf("outdated = %+v, want the old index with its replacement building", r.Outdated)
	}
}

func TestCompareOfAnUpToDateCollectionIsNotStale(t *testing.T) {
	existing := []Existing{
		{Name: "_id_", Keys: "_id:1"},
		{Name: "room_id_1_state_1", Keys: "room_id:1,state:1"},
		{Name: "user_id_1_state_1_ver_1", Keys: "user_id:1,state:1,ver:1"},
	}
	r, err := Compare(membersDeclared, membersReplaced, existing)
	if err != nil || r.Stale() || len(r.Lines()) != 0 {
		t.Fatalf("Compare = %+v, %v; want nothing to report", r, err)
	}
	if got := (Report{Collection: "members", Absent: true}).Lines(); !slices.Equal(got, []string{"members absent, core creates it at startup"}) {
		t.Fatalf("absent Lines = %q", got)
	}
}

func TestDropPlanNeedsEveryReplacementReady(t *testing.T) {
	ready := Report{Collection: "members", Outdated: []Outdated{
		{Replacement: membersReplaced[0], State: ReplacementReady},
		{Replacement: membersReplaced[2], State: ReplacementRetired},
	}}
	plan, err := dropPlan([]Report{ready})
	if err != nil || !slices.Equal(plan, []Replacement{membersReplaced[0], membersReplaced[2]}) {
		t.Fatalf("dropPlan(ready) = %+v, %v", plan, err)
	}
	for _, state := range []ReplacementState{ReplacementBuilding, ReplacementAbsent} {
		blocked := Report{Collection: "message_interactions", Outdated: []Outdated{{Replacement: Replacement{Collection: "message_interactions", Old: "a_1", New: "a_1_b_1"}, State: state}}}
		if plan, err := dropPlan([]Report{ready, blocked}); !errors.Is(err, ErrReplacementNotReady) || plan != nil {
			t.Fatalf("dropPlan(%s) = %+v, %v; want ErrReplacementNotReady and nothing to drop", state, plan, err)
		}
	}
}

func TestExistingOfReadsReadyAndBuildingEntries(t *testing.T) {
	spec := bson.D{{Key: "v", Value: 2}, {Key: "key", Value: bson.D{{Key: "a", Value: 1}, {Key: "b", Value: -1}}}, {Key: "name", Value: "a_1_b_-1"}}
	docs := []struct {
		doc  bson.D
		want Existing
	}{
		{spec, Existing{Name: "a_1_b_-1", Keys: "a:1,b:-1"}},
		{bson.D{{Key: "spec", Value: spec}}, Existing{Name: "a_1_b_-1", Keys: "a:1,b:-1"}},
		{bson.D{{Key: "spec", Value: spec}, {Key: "indexBuildInfo", Value: bson.D{{Key: "buildUUID", Value: "x"}}}}, Existing{Name: "a_1_b_-1", Keys: "a:1,b:-1", Building: true}},
	}
	for _, d := range docs {
		raw, _ := bson.Marshal(d.doc)
		if got, err := existingOf(raw); err != nil || got != d.want {
			t.Errorf("existingOf(%v) = %+v, %v; want %+v", d.doc, got, err, d.want)
		}
	}
	raw, _ := bson.Marshal(bson.D{{Key: "name", Value: "x"}})
	if _, err := existingOf(raw); err == nil {
		t.Fatal("existingOf(no key) = nil error, want an error")
	}
}
