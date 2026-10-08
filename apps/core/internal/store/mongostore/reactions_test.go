package mongostore

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestSetPipelineKeepsEveryChangedFieldWhenTheEmojiIsAlreadySet(t *testing.T) {
	r := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	data, err := bson.Marshal(setReaction(r, 7)[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := bson.Raw(data)
	for _, field := range []string{"previous_emoji", "emoji", "ver", "updated_at"} {
		cond := raw.Lookup("$set", field)
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$eq", "0").StringValueOK(); !ok || got != "$emoji" {
			t.Fatalf("$set.%s if = %s, want {$eq: [$emoji, emoji]}", field, cond)
		}
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$eq", "1", "$literal").StringValueOK(); !ok || got != "$e" {
			t.Fatalf("$set.%s if = %s, want the emoji as a literal", field, cond)
		}
		if got, ok := raw.Lookup("$set", field, "$cond", "1").StringValueOK(); !ok || got != "$"+field {
			t.Fatalf("$set.%s then = %s, want $%s", field, cond, field)
		}
	}
	if got, ok := raw.Lookup("$set", "emoji", "$cond", "2", "$literal").StringValueOK(); !ok || got != "$e" {
		t.Fatalf("$set.emoji else = %s, want {$literal: $e}", raw.Lookup("$set", "emoji"))
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
