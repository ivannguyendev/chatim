package mongostore

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleReactionDoc() interactionDoc {
	key := keys.Msg(7_340_000_001, 3, 42)
	return interactionDoc{
		ID: keys.InteractionUser(key, keys.ReactionKind, "bob"), Key: key, Room: 7_340_000_001, Tenant: "acme", Kind: "reaction",
		Actor: "bob", Value: "❤️", Prev: "👍", State: interactionLive, Ver: 2, CreatedAt: codecTime, UpdatedAt: codecTime,
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
	d := sampleReactionDoc()
	d.State, d.Prev, d.Ver = interactionRemoved, "❤️", 3
	gone, err := decodeReaction(d)
	if err != nil || gone.Emoji != "" || gone.Prev != "❤️" || gone.N != 3 {
		t.Fatalf("decodeReaction(removed) = %+v, %v; want no emoji, previous ❤️, change 3", gone, err)
	}
}

func TestDecodeBookmarkAndReplyReadTheirKeys(t *testing.T) {
	key := keys.Msg(7_340_000_001, 0, 40)
	b, err := decodeBookmark(interactionDoc{ID: keys.InteractionUser(key, keys.BookmarkKind, "minh"), Tenant: "acme", State: interactionRemoved, Ver: 2, UpdatedAt: codecTime})
	if err != nil || b.Room != 7_340_000_001 || b.Seq != 40 || b.User != "minh" || b.On || b.Ver != 2 || b.Tenant != "acme" || !b.At.Equal(codecTime) {
		t.Fatalf("decodeBookmark = %+v, %v", b, err)
	}
	r, err := decodeReply(interactionDoc{ID: keys.InteractionReply(key, 0, 57), Tenant: "acme", Actor: "lan", ReplySeq: 57, State: interactionLive, Ver: 1, UpdatedAt: codecTime})
	want := domain.Reply{Parent: domain.MsgKey{Room: 7_340_000_001, Seq: 40}, Room: 7_340_000_001, Seq: 57, Tenant: "acme", From: "lan", Live: true, Ver: 1, At: codecTime}
	if err != nil || r != want {
		t.Fatalf("decodeReply = %+v, %v; want %+v", r, err, want)
	}
}

func TestDecodeInteractionsRejectCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*interactionDoc){
		"message-only id": func(d *interactionDoc) { d.ID = keys.Msg(1, 0, 1) },
		"old reaction id": func(d *interactionDoc) { d.ID = append(keys.Msg(1, 0, 1), "bob"...) },
		"bookmark id":     func(d *interactionDoc) { d.ID = keys.InteractionUser(keys.Msg(1, 0, 1), keys.BookmarkKind, "bob") },
		"user with a dot": func(d *interactionDoc) { d.ID = keys.InteractionUser(keys.Msg(1, 0, 1), keys.ReactionKind, "a.b") },
		"negative ver":    func(d *interactionDoc) { d.Ver = -1 },
		"ver above max":   func(d *interactionDoc) { d.Ver = math.MaxUint32 + 1 },
		"unknown state":   func(d *interactionDoc) { d.State = 3 },
	} {
		d := sampleReactionDoc()
		mutate(&d)
		if _, err := decodeReaction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeReaction = %v, want errCorrupt", name, err)
		}
	}
	reply := interactionDoc{ID: keys.InteractionReply(keys.Msg(1, 0, 1), 0, 2), Actor: "a.b", State: interactionLive, Ver: 1}
	if _, err := decodeReply(reply); !errors.Is(err, errCorrupt) {
		t.Errorf("decodeReply(bad author) = %v, want errCorrupt", err)
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
		"count too large":  {Counts: []countDoc{{Emoji: "👍", N: math.MaxUint32 + 1}}, Version: 1},
	} {
		if _, err := decodeSummary(&d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeSummary = %v, want errCorrupt", name, err)
		}
	}
	leftover := reactionsDoc{Counts: []countDoc{{Emoji: "❤️", N: 1}, {Emoji: "👍", N: 0}, {Emoji: "😂", N: -1}}, Version: 3}
	if got, err := decodeSummary(&leftover); err != nil || got.Version != 3 || !got.Unsettled || !slices.Equal(got.Counts, []domain.ReactionCount{{Emoji: "❤️", Count: 1}}) {
		t.Errorf("decodeSummary(entries at or below zero) = %+v, %v; want only ❤️ at version 3, unsettled", got, err)
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
	head := interactionFields(store.ReactionKeyOf(r), 7, r.Tenant, keys.ReactionKind, r.User)
	data, err := bson.Marshal(setReaction(head, r)[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := bson.Raw(data)
	if got, ok := raw.Lookup("$set", "actor_id", "$literal").StringValueOK(); !ok || got != "alice" {
		t.Fatalf("$set.actor_id = %s, want {$literal: alice}", raw.Lookup("$set", "actor_id"))
	}
	if got, ok := raw.Lookup("$set", "kind", "$literal").StringValueOK(); !ok || got != "reaction" {
		t.Fatalf("$set.kind = %s, want {$literal: reaction}", raw.Lookup("$set", "kind"))
	}
	if _, k, ok := raw.Lookup("$set", "message_key").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("$set.message_key = %s, want the 24-byte message key", raw.Lookup("$set", "message_key"))
	}
}
