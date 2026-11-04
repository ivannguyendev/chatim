package mongobound_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/pkg/core/mongobound"
)

func triple(a, b, c uint64) []byte {
	return binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(binary.BigEndian.AppendUint64(nil, a), b), c)
}

func prefixed(prefix uint64, suffix string) []byte {
	return append(binary.BigEndian.AppendUint64(nil, prefix), suffix...)
}

func idRange[K []byte | string](lo, hi K, extra ...bson.E) bson.D {
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

func TestIDsUsesEqualityForOneKey(t *testing.T) {
	k := triple(7, 0, 3)
	expectFilter(t, "one key", mongobound.IDs([][]byte{k, k}), bson.D{{Key: "_id", Value: k}})
	state := bson.E{Key: "state", Value: int64(1)}
	expectFilter(t, "one key with a predicate", mongobound.IDs([][]byte{k}, state), bson.D{{Key: "_id", Value: k}, state})
}

func TestIDsMergesConsecutiveSeqsIntoOneRange(t *testing.T) {
	got := mongobound.IDs([][]byte{triple(7, 0, 12), triple(7, 0, 10), triple(7, 0, 11)})
	expectFilter(t, "a run", got, idRange(triple(7, 0, 10), triple(7, 0, 12)))
}

func TestIDsKeepsOneBranchPerRoomRunAndPredicatesInEachBranch(t *testing.T) {
	state := bson.E{Key: "state", Value: int64(1)}
	got := mongobound.IDs([][]byte{triple(9, 0, 5), triple(7, 0, 2), triple(7, 0, 1), triple(7, 0, 40), triple(7, 3, 41)}, state)
	want := bson.D{{Key: "$or", Value: bson.A{
		idRange(triple(7, 0, 1), triple(7, 0, 2), state),
		idRange(triple(7, 0, 40), triple(7, 0, 40), state),
		idRange(triple(7, 3, 41), triple(7, 3, 41), state),
		idRange(triple(9, 0, 5), triple(9, 0, 5), state),
	}}}
	expectFilter(t, "rooms and threads", got, want)
}

func TestIDsOrdersShorterKeysFirstAndNeverMergesAcrossLengths(t *testing.T) {
	got := mongobound.IDs([][]byte{prefixed(7, "ab"), prefixed(7, "b"), prefixed(9, "c"), prefixed(7, "a")})
	want := bson.D{{Key: "$or", Value: bson.A{
		idRange(prefixed(7, "a"), prefixed(7, "b")),
		idRange(prefixed(9, "c"), prefixed(9, "c")),
		idRange(prefixed(7, "ab"), prefixed(7, "ab")),
	}}}
	expectFilter(t, "mixed lengths", got, want)
}

func TestIDsNeverMergesPastTheLastByteValue(t *testing.T) {
	top := []byte{0xff, 0xff}
	got := mongobound.IDs([][]byte{top, {0x00, 0x00}})
	want := bson.D{{Key: "$or", Value: bson.A{idRange([]byte{0x00, 0x00}, []byte{0x00, 0x00}), idRange(top, top)}}}
	expectFilter(t, "top key", got, want)
}

func TestIDsDoesNotChangeItsInput(t *testing.T) {
	in := [][]byte{triple(7, 0, 2), triple(7, 0, 1)}
	before := [][]byte{bytes.Clone(in[0]), bytes.Clone(in[1])}
	mongobound.IDs(in)
	if !bytes.Equal(in[0], before[0]) || !bytes.Equal(in[1], before[1]) {
		t.Error("IDs changed the caller's keys")
	}
}

func TestIDsMatchesNothingForNoKeys(t *testing.T) {
	state := bson.E{Key: "state", Value: int64(1)}
	want := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}}, state}
	expectFilter(t, "no keys", mongobound.IDs(nil, state), want)
}
