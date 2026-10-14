package mongostore

import (
	"bytes"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

func idRange(lo, hi []byte, extra ...bson.E) bson.D {
	return append(bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}}}, extra...)
}

func expectFilter(t *testing.T, name string, got, want bson.D) {
	t.Helper()
	g, err := bson.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal got: %v", name, err)
	}
	w, err := bson.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal want: %v", name, err)
	}
	if !bytes.Equal(g, w) {
		t.Errorf("%s:\n got  %v\n want %v", name, bson.Raw(g), bson.Raw(w))
	}
}

func TestBoundedIDsUsesEqualityForOneKey(t *testing.T) {
	k := keys.Msg(7, 0, 3)
	expectFilter(t, "one key", boundedIDs([][]byte{k, k}), bson.D{{Key: "_id", Value: k}})
	state := bson.E{Key: "state", Value: int64(1)}
	expectFilter(t, "one key with a predicate", boundedIDs([][]byte{k}, state), bson.D{{Key: "_id", Value: k}, state})
}

func TestBoundedIDsMergesConsecutiveSeqsIntoOneRange(t *testing.T) {
	got := boundedIDs([][]byte{keys.Msg(7, 0, 12), keys.Msg(7, 0, 10), keys.Msg(7, 0, 11)})
	expectFilter(t, "a run", got, idRange(keys.Msg(7, 0, 10), keys.Msg(7, 0, 12)))
}

func TestBoundedIDsKeepsOneBranchPerRoomRunAndPredicatesInEachBranch(t *testing.T) {
	state := bson.E{Key: "state", Value: int64(1)}
	got := boundedIDs([][]byte{keys.Msg(9, 0, 5), keys.Msg(7, 0, 2), keys.Msg(7, 0, 1), keys.Msg(7, 0, 40), keys.Msg(7, 3, 41)}, state)
	want := bson.D{{Key: "$or", Value: bson.A{
		idRange(keys.Msg(7, 0, 1), keys.Msg(7, 0, 2), state),
		idRange(keys.Msg(7, 0, 40), keys.Msg(7, 0, 40), state),
		idRange(keys.Msg(7, 3, 41), keys.Msg(7, 3, 41), state),
		idRange(keys.Msg(9, 0, 5), keys.Msg(9, 0, 5), state),
	}}}
	expectFilter(t, "rooms and threads", got, want)
}

func TestBoundedIDsOrdersShorterKeysFirstAndNeverMergesAcrossLengths(t *testing.T) {
	got := boundedIDs([][]byte{keys.Member(7, "ab"), keys.Member(7, "b"), keys.Member(9, "c"), keys.Member(7, "a")})
	want := bson.D{{Key: "$or", Value: bson.A{
		idRange(keys.Member(7, "a"), keys.Member(7, "b")),
		idRange(keys.Member(9, "c"), keys.Member(9, "c")),
		idRange(keys.Member(7, "ab"), keys.Member(7, "ab")),
	}}}
	expectFilter(t, "mixed lengths", got, want)
}

func TestBoundedIDsNeverMergesPastTheLastByteValue(t *testing.T) {
	top := []byte{0xff, 0xff}
	got := boundedIDs([][]byte{top, {0x00, 0x00}})
	want := bson.D{{Key: "$or", Value: bson.A{idRange([]byte{0x00, 0x00}, []byte{0x00, 0x00}), idRange(top, top)}}}
	expectFilter(t, "top key", got, want)
}

func TestBoundedIDsDoesNotChangeItsInput(t *testing.T) {
	in := [][]byte{keys.Msg(7, 0, 2), keys.Msg(7, 0, 1)}
	before := [][]byte{bytes.Clone(in[0]), bytes.Clone(in[1])}
	boundedIDs(in)
	if !bytes.Equal(in[0], before[0]) || !bytes.Equal(in[1], before[1]) {
		t.Error("boundedIDs changed the caller's keys")
	}
}
