package mongostore

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func pipelineStage(t *testing.T, p []bson.D) bson.Raw {
	t.Helper()
	data, err := bson.Marshal(p[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return bson.Raw(data)
}

func TestSetPipelineKeepsEveryChangedFieldWhenTheEmojiIsAlreadySet(t *testing.T) {
	r := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	raw := pipelineStage(t, setReaction(interactionFields(store.ReactionKeyOf(r), 7, r.Tenant, keys.ReactionKind, r.User), r))
	for _, field := range []string{"value", "previous_value", "state", "ver", "updated_at"} {
		cond := raw.Lookup("$set", field)
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$and", "0", "$eq", "0").StringValueOK(); !ok || got != "$state" {
			t.Fatalf("$set.%s if = %s, want {$and: [{$eq: [$state, 1]}, ...]}", field, cond)
		}
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$and", "1", "$eq", "1", "$literal").StringValueOK(); !ok || got != "$e" {
			t.Fatalf("$set.%s if = %s, want the emoji as a literal", field, cond)
		}
		if got, ok := raw.Lookup("$set", field, "$cond", "1").StringValueOK(); !ok || got != "$"+field {
			t.Fatalf("$set.%s then = %s, want $%s", field, cond, field)
		}
	}
	if got, ok := raw.Lookup("$set", "value", "$cond", "2", "$literal").StringValueOK(); !ok || got != "$e" {
		t.Fatalf("$set.value else = %s, want {$literal: $e}", raw.Lookup("$set", "value"))
	}
	if got, ok := raw.Lookup("$set", "created_at", "$ifNull", "0").StringValueOK(); !ok || got != "$created_at" {
		t.Fatalf("$set.created_at = %s, want the first write time kept", raw.Lookup("$set", "created_at"))
	}
}

func TestBookmarkAndRemovePipelinesKeepALiveDocAndCountChanges(t *testing.T) {
	key := store.MsgKey{Room: 7, Seq: 1}
	raw := pipelineStage(t, setBookmark(interactionFields(key, 7, "acme", keys.BookmarkKind, "alice"), codecTime))
	for _, field := range []string{"ver", "updated_at"} {
		if got, ok := raw.Lookup("$set", field, "$cond", "1").StringValueOK(); !ok || got != "$"+field {
			t.Fatalf("$set.%s = %s, want it kept while the bookmark is live", field, raw.Lookup("$set", field))
		}
	}
	if _, err := raw.LookupErr("$set", "value"); err == nil {
		t.Fatalf("bookmark pipeline %s sets a value", raw)
	}
	removed := pipelineStage(t, removeInteraction(codecTime, true))
	if got, ok := removed.Lookup("$set", "previous_value").StringValueOK(); !ok || got != "$value" {
		t.Fatalf("reaction removal %s, want previous_value = $value", removed)
	}
	if got, ok := removed.Lookup("$set", "state").AsInt64OK(); !ok || got != interactionRemoved {
		t.Fatalf("removal %s, want state %d", removed, interactionRemoved)
	}
	if _, err := pipelineStage(t, removeInteraction(codecTime, false)).LookupErr("$set", "previous_value"); err == nil {
		t.Fatal("bookmark removal sets previous_value")
	}
}

func TestSetResultFollowsTheDocBefore(t *testing.T) {
	x := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "❤️", At: codecTime.Add(1500 * time.Microsecond)}
	want := x
	want.At = codecTime.Add(time.Millisecond)
	first := setResult(x, domain.Reaction{})
	want.Prev, want.N = "", 1
	if !first.At.Equal(want.At) || first.At.Location() != time.UTC {
		t.Fatalf("first At = %v, want %v in UTC", first.At, want.At)
	}
	first.At = want.At
	if first != want {
		t.Fatalf("first = %+v, want %+v", first, want)
	}
	before := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "👍", Prev: "😂", N: 4, At: codecTime}
	next := setResult(x, before)
	want.Prev, want.N = "👍", 5
	next.At = want.At
	if next != want {
		t.Fatalf("next = %+v, want %+v", next, want)
	}
}
