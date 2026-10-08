package mongostore

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleReactionDoc() reactionDoc {
	return reactionDoc{
		ID: keys.Reaction(7_340_000_001, 3, 42, "bob"), Key: keys.Msg(7_340_000_001, 3, 42), Room: 7_340_000_001,
		Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: codecTime,
	}
}

func TestDecodeReactionReadsTheKeyAndChangeNumber(t *testing.T) {
	got, err := decodeReaction(sampleReactionDoc())
	want := domain.Reaction{Room: 7_340_000_001, Thread: 3, Seq: 42, Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: codecTime}
	if err != nil || !got.At.Equal(want.At) {
		t.Fatalf("decodeReaction = %+v, %v; want %+v", got, err, want)
	}
	got.At = want.At
	if got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
}

func TestDecodeReactionRejectsCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*reactionDoc){
		"message-only id": func(d *reactionDoc) { d.ID = keys.Msg(1, 0, 1) },
		"user with a dot": func(d *reactionDoc) { d.ID = keys.Reaction(1, 0, 1, "a.b") },
		"negative n":      func(d *reactionDoc) { d.N = -1 },
		"n above uint32":  func(d *reactionDoc) { d.N = math.MaxUint32 + 1 },
	} {
		d := sampleReactionDoc()
		mutate(&d)
		if _, err := decodeReaction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeReaction = %v, want errCorrupt", name, err)
		}
	}
}

func TestSummaryCodecRoundTrip(t *testing.T) {
	s := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "$x", Count: 1}}, Version: 7}
	doc, err := encodeSummary(s)
	if err != nil {
		t.Fatalf("encodeSummary: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"c", "v"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("c", "1", "e").StringValue(); e != "$x" {
		t.Fatalf("second count emoji = %q, want $x stored as a value", e)
	}
	got, err := decodeSummary(&back)
	if err != nil || got.Version != 7 || !slices.Equal(got.Counts, s.Counts) {
		t.Fatalf("decodeSummary = %+v, %v; want %+v", got, err, s)
	}
	if got, err := decodeSummary(nil); err != nil || got.Version != 0 || got.Counts != nil {
		t.Fatalf("decodeSummary(nil) = %+v, %v; want the zero summary", got, err)
	}
	if _, err := encodeSummary(domain.ReactionSummary{Version: math.MaxInt64 + 1}); err == nil {
		t.Fatal("encodeSummary accepted a version above max int64")
	}
	for name, d := range map[string]reactionsDoc{
		"negative version": {Version: -1},
		"negative count":   {Counts: []countDoc{{Emoji: "👍", N: -1}}, Version: 1},
	} {
		if _, err := decodeSummary(&d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeSummary = %v, want errCorrupt", name, err)
		}
	}
}

func TestMessageCodecReadsReactionsButInsertNeverWritesThem(t *testing.T) {
	m := sampleMessage()
	m.Reactions = domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}
	doc, err := encodeMessage(m)
	if err != nil || doc.Reactions != nil {
		t.Fatalf("encodeMessage = %+v, %v; want no rx", doc.Reactions, err)
	}
	if _, raw := roundTrip(t, doc); !slices.Equal(fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts"}) {
		t.Fatalf("fields = %v, want the 7 fields of a new message", fieldNames(t, raw))
	}
	rx, err := encodeSummary(m.Reactions)
	if err != nil {
		t.Fatalf("encodeSummary: %v", err)
	}
	doc.Reactions = &rx
	got, err := decodeMessage(doc)
	if err != nil || !reflect.DeepEqual(got.Reactions, m.Reactions) {
		t.Fatalf("decodeMessage reactions = %+v, %v; want %+v", got.Reactions, err, m.Reactions)
	}
}

func TestSetPipelineTakesStringsLiterally(t *testing.T) {
	r := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	data, err := bson.Marshal(setReaction(r, 7)[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := bson.Raw(data)
	if got, ok := raw.Lookup("$set", "user_id", "$literal").StringValueOK(); !ok || got != "alice" {
		t.Fatalf("$set.user_id = %s, want {$literal: alice}", raw.Lookup("$set", "user_id"))
	}
	if _, k, ok := raw.Lookup("$set", "message_key").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("$set.message_key = %s, want the 24-byte message key", raw.Lookup("$set", "message_key"))
	}
}
