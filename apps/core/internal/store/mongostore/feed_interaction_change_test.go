package mongostore

import (
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func reactionUpdate(t *testing.T, id any, fields bson.D) changeDoc {
	t.Helper()
	key, err := bson.Marshal(bson.D{{Key: "_id", Value: id}})
	if err != nil {
		t.Fatalf("Marshal key: %v", err)
	}
	updated, err := bson.Marshal(fields)
	if err != nil {
		t.Fatalf("Marshal fields: %v", err)
	}
	return changeDoc{
		OperationType: "update", WallTime: codecTime, NS: changeNS{Coll: interactionsCollection},
		DocumentKey: changeKey{ID: bson.Raw(key).Lookup("_id")}, UpdateDescription: changeUpdate{UpdatedFields: updated},
	}
}

func TestDecodeChangeReadsReactionUpdatesFromTheKey(t *testing.T) {
	id := keys.InteractionUser(keys.Msg(7_340_000_001, 3, 42), keys.ReactionKind, "bob")
	want := domain.Reaction{Room: 7_340_000_001, Thread: 3, Seq: 42, User: "bob", N: 2}
	for name, n := range map[string]any{"int32": int32(2), "int64": int64(2), "double": float64(2)} {
		got, err := decodeChange(reactionUpdate(t, id, bson.D{{Key: "previous_value", Value: "👍"}, {Key: "state", Value: int32(2)}, {Key: "ver", Value: n}}))
		if err != nil || got.Kind != store.ReactionChanged || got.Reaction != want || !got.CommittedAt.Equal(codecTime) || got.Msg.Seq != 0 {
			t.Fatalf("%s: update change = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestDecodeChangeReadsReactionInsertsAndReplacesFromTheDocument(t *testing.T) {
	want, err := decodeReaction(sampleReactionDoc())
	if err != nil {
		t.Fatalf("decodeReaction: %v", err)
	}
	for _, op := range []string{"insert", "replace"} {
		ev := changeOn(t, interactionsCollection, sampleReactionDoc())
		ev.OperationType = op
		got, err := decodeChange(ev)
		if err != nil || got.Kind != store.ReactionChanged || !got.Reaction.At.Equal(want.At) {
			t.Fatalf("%s change = %+v, %v; want %+v", op, got, err, want)
		}
		got.Reaction.At = want.At
		if got.Reaction != want {
			t.Fatalf("%s change reaction = %+v, want %+v", op, got.Reaction, want)
		}
	}
}

func TestDecodeChangeReadsPinFacts(t *testing.T) {
	a := samplePinAction()
	doc, err := encodePinAction(a)
	if err != nil {
		t.Fatalf("encodePinAction: %v", err)
	}
	got, err := decodeChange(changeOn(t, pinActionsCollection, doc))
	if err != nil || got.Kind != store.PinInserted || !got.Pin.At.Equal(a.At) || got.Reaction.N != 0 {
		t.Fatalf("pin change = %+v, %v; want %+v", got, err, a)
	}
	got.Pin.At = a.At
	if got.Pin != a {
		t.Fatalf("pin change fact = %+v, want %+v", got.Pin, a)
	}
}

func TestDecodeChangeRejectsBrokenReactionAndPinChanges(t *testing.T) {
	id := keys.InteractionUser(keys.Msg(7_340_000_001, 0, 42), keys.ReactionKind, "bob")
	cases := map[string]changeDoc{
		"update without ver":      reactionUpdate(t, id, bson.D{{Key: "value", Value: "x"}}),
		"update with the old n":   reactionUpdate(t, id, bson.D{{Key: "n", Value: int32(2)}}),
		"update with ver 0":       reactionUpdate(t, id, bson.D{{Key: "ver", Value: int32(0)}}),
		"update with ver too big": reactionUpdate(t, id, bson.D{{Key: "ver", Value: int64(math.MaxUint32) + 1}}),
		"update with string ver":  reactionUpdate(t, id, bson.D{{Key: "ver", Value: "2"}}),
		"update of a short key":   reactionUpdate(t, keys.Msg(1, 0, 1), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a bad user":    reactionUpdate(t, keys.InteractionUser(keys.Msg(1, 0, 1), keys.ReactionKind, "a.b"), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of an old key":    reactionUpdate(t, append(keys.Msg(1, 0, 1), "bob"...), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a string key":  reactionUpdate(t, "x", bson.D{{Key: "ver", Value: int32(2)}}),
		"insert of a bad key":     changeOn(t, interactionsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"insert of a bad pin":     changeOn(t, pinActionsCollection, bson.D{{Key: "_id", Value: []byte{1}}}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}

func TestDecodeChangeSkipsBookmarkAndReplyChanges(t *testing.T) {
	key := keys.Msg(7_340_000_001, 0, 40)
	bookmark, reply := keys.InteractionUser(key, keys.BookmarkKind, "minh"), keys.InteractionReply(key, 0, 57)
	cases := map[string]changeDoc{
		"bookmark update": reactionUpdate(t, bookmark, bson.D{{Key: "ver", Value: int32(2)}}),
		"reply update":    reactionUpdate(t, reply, bson.D{{Key: "ver", Value: int32(2)}}),
		"bookmark insert": changeOn(t, interactionsCollection, interactionDoc{ID: bookmark, Actor: "minh", State: interactionLive, Ver: 1}),
		"reply insert":    changeOn(t, interactionsCollection, interactionDoc{ID: reply, Actor: "lan", State: interactionLive, Ver: 1}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errSkipChange) {
			t.Errorf("%s: decodeChange = %v, want errSkipChange", name, err)
		}
	}
}
