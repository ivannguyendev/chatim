package mongobound_test

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/pkg/mongobound"
)

func TestStringIDsUsesEqualityForOneKey(t *testing.T) {
	expectFilter(t, "one key", mongobound.StringIDs([]string{"t│a", "t│a"}), bson.D{{Key: "_id", Value: "t│a"}})
	state := bson.E{Key: "state", Value: int64(1)}
	expectFilter(t, "one key with a predicate", mongobound.StringIDs([]string{"t│a"}, state), bson.D{{Key: "_id", Value: "t│a"}, state})
}

func TestStringIDsMergesTheNextStringInByteOrderIntoOneRange(t *testing.T) {
	got := mongobound.StringIDs([]string{"ab\x00\x00", "ab", "ab\x00"})
	expectFilter(t, "a run", got, idRange("ab", "ab\x00\x00"))
}

func TestStringIDsKeepsDisjointKeysAsRangesWithPredicatesInEachBranch(t *testing.T) {
	state := bson.E{Key: "state", Value: int64(1)}
	got := mongobound.StringIDs([]string{"b", "ab", "a", "a\x00"}, state)
	want := bson.D{{Key: "$or", Value: bson.A{
		idRange("a", "a\x00", state),
		idRange("ab", "ab", state),
		idRange("b", "b", state),
	}}}
	expectFilter(t, "disjoint keys", got, want)
}

func TestStringIDsNeverMergesKeysThatOnlyLookConsecutive(t *testing.T) {
	got := mongobound.StringIDs([]string{"ab", "ac", "a"})
	want := bson.D{{Key: "$or", Value: bson.A{idRange("a", "a"), idRange("ab", "ab"), idRange("ac", "ac")}}}
	expectFilter(t, "same length neighbours", got, want)
}

func TestStringIDsDoesNotChangeItsInput(t *testing.T) {
	in := []string{"b", "a"}
	mongobound.StringIDs(in)
	if !slices.Equal(in, []string{"b", "a"}) {
		t.Error("StringIDs changed the caller's keys")
	}
}

func TestStringIDsMatchesNothingForNoKeys(t *testing.T) {
	state := bson.E{Key: "state", Value: int64(1)}
	want := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}}, state}
	expectFilter(t, "no keys", mongobound.StringIDs(nil, state), want)
}

func TestRunsReportsEachRunOfBinaryKeys(t *testing.T) {
	got := mongobound.Runs([][]byte{{0, 3}, {0, 1}, {0, 2}, {0, 9}})
	want := []mongobound.Run[[]byte]{{Lo: []byte{0, 1}, Hi: []byte{0, 3}}, {Lo: []byte{0, 9}, Hi: []byte{0, 9}}}
	if len(got) != len(want) {
		t.Fatalf("Runs = %v, want %v", got, want)
	}
	for i := range want {
		if string(got[i].Lo) != string(want[i].Lo) || string(got[i].Hi) != string(want[i].Hi) {
			t.Errorf("run %d = %v, want %v", i, got[i], want[i])
		}
	}
	expectFilter(t, "run filter", got[0].Filter(nil), idRange([]byte{0, 1}, []byte{0, 3}))
}
