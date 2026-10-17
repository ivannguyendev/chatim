package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestInsertedMessageCarriesTheCreatedFlagAtItsSendTime(t *testing.T) {
	doc, err := encodeMessage(sampleMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	encoded, err := bson.Marshal(flaggedInsert(doc))
	raw := bson.Raw(encoded)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", stateField, stateAtField}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if got := raw.Lookup(stateField).Int32(); got != int32(store.BitMsgCreated) {
		t.Fatalf("%s = %d, want %d", stateField, got, store.BitMsgCreated)
	}
	if got := raw.Lookup(stateAtField).Time().UTC(); !got.Equal(codecTime) {
		t.Fatalf("%s = %v, want %v", stateAtField, got, codecTime)
	}
}

func TestCountWritesRaiseTheirBitInTheSameStage(t *testing.T) {
	count := bson.D{{Key: "n", Value: int64(2)}, {Key: "v", Value: int64(1)}}
	for name, c := range map[string]struct {
		pipeline mongo.Pipeline
		field    string
		bit      store.StateBit
	}{
		"reaction deltas": {reactionDeltaPipeline([]store.EmojiDelta{{Emoji: "👍", Delta: 1}}), "rx", store.BitReactionCounts},
		"reply delta":     {replyDeltaPipeline(1), "rc", store.BitReplyCounts},
		"set reactions":   {flaggedSet(store.BitReactionCounts, setLiteral("rx", count)), "rx", store.BitReactionCounts},
		"set reply count": {flaggedSet(store.BitReplyCounts, setLiteral("rc", count)), "rc", store.BitReplyCounts},
	} {
		if len(c.pipeline) != 1 {
			t.Fatalf("%s: %d stages, want 1", name, len(c.pipeline))
		}
		stage, err := bson.Marshal(c.pipeline[0])
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		set := bson.Raw(stage).Lookup("$set").Document()
		if _, err := set.LookupErr(c.field); err != nil {
			t.Fatalf("%s: $set %s has no %s", name, set, c.field)
		}
		bits := set.Lookup(stateField, "$bitOr").Array().Index(1).Int32()
		if bits != int32(c.bit) {
			t.Fatalf("%s: $bitOr %d, want %d", name, bits, c.bit)
		}
		if _, err := set.LookupErr(stateAtField, "$min"); err != nil {
			t.Fatalf("%s: $set %s has no %s $min", name, set, stateAtField)
		}
	}
}
