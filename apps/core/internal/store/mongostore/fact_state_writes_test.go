package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func marshalRaw(t *testing.T, v any) bson.Raw {
	t.Helper()
	data, err := bson.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return bson.Raw(data)
}

func assertFlagAt(t *testing.T, op string, raw bson.Raw, at time.Time) {
	t.Helper()
	if got, ok := raw.Lookup(stateField).Int32OK(); !ok || got != 1 {
		t.Fatalf("%s: %s = %s, want 1", op, stateField, raw.Lookup(stateField))
	}
	if got, ok := raw.Lookup(stateAtField).TimeOK(); !ok || !got.Equal(at) {
		t.Fatalf("%s: %s = %s, want %v", op, stateAtField, raw.Lookup(stateAtField), at)
	}
}

func TestEditFactsCarryTheirFlagAndTheOriginalRowDoesNot(t *testing.T) {
	e := sampleEdit()
	doc, err := editInsert(e)
	if err != nil {
		t.Fatalf("editInsert: %v", err)
	}
	raw := marshalRaw(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "created_at", stateField, stateAtField}; !slices.Equal(got, want) {
		t.Fatalf("fact fields = %v, want %v", got, want)
	}
	assertFlagAt(t, "fact", raw, e.At)
	e.Version, e.Kind = 0, domain.EditOriginal
	if doc, err = editInsert(e); err != nil {
		t.Fatalf("editInsert(original): %v", err)
	}
	if got, want := fieldNames(t, marshalRaw(t, doc)), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("original fields = %v, want %v", got, want)
	}
}

func TestPinFactCarriesItsFlagAtItsTime(t *testing.T) {
	a := samplePinAction()
	doc, err := pinInsert(a)
	if err != nil {
		t.Fatalf("pinInsert: %v", err)
	}
	raw := marshalRaw(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "action", "thread_root", "seq", "created_by", "created_at", stateField, stateAtField}; !slices.Equal(got, want) {
		t.Fatalf("pin fields = %v, want %v", got, want)
	}
	assertFlagAt(t, "pin", raw, a.At)
}

func TestFirstHideSetsTheFlagOnInsertOnly(t *testing.T) {
	raw := marshalRaw(t, hideUpdate(codecTime))
	if got := fieldNames(t, raw); !slices.Equal(got, []string{"$setOnInsert"}) {
		t.Fatalf("hide update operators = %v, want only $setOnInsert", got)
	}
	insert := raw.Lookup("$setOnInsert").Document()
	if got, want := fieldNames(t, insert), []string{"created_at", stateField, stateAtField}; !slices.Equal(got, want) {
		t.Fatalf("$setOnInsert fields = %v, want %v", got, want)
	}
	assertFlagAt(t, "hide", insert, codecTime)
}

func TestInteractionUpsertsRaiseTheFlagOnlyInTheChangingBranch(t *testing.T) {
	r := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	key := store.MsgKey{Room: 7, Seq: 1}
	for name, p := range map[string][]bson.D{
		"reaction": setReaction(interactionFields(key, 7, r.Tenant, keys.ReactionKind, r.User), r),
		"bookmark": setBookmark(interactionFields(key, 7, "acme", keys.BookmarkKind, "alice"), codecTime),
	} {
		set := pipelineStage(t, p).Lookup("$set").Document()
		for _, field := range []string{stateField, stateAtField} {
			if got, ok := set.Lookup(field, "$cond", "1").StringValueOK(); !ok || got != "$"+field {
				t.Fatalf("%s: $set.%s = %s, want it kept when nothing changes", name, field, set.Lookup(field))
			}
		}
		if got := set.Lookup(stateField, "$cond", "2", "$bitOr").Array().Index(1).Int32(); got != int32(store.BitInteraction) {
			t.Fatalf("%s: changing branch $bitOr %d, want %d", name, got, store.BitInteraction)
		}
		if _, err := set.LookupErr(stateAtField, "$cond", "2", "$min"); err != nil {
			t.Fatalf("%s: changing branch of %s = %s, want $min", name, stateAtField, set.Lookup(stateAtField))
		}
	}
}

func TestRemovalsRaiseTheFlagAndReplyRemovalDoesNot(t *testing.T) {
	for _, keepValue := range []bool{true, false} {
		set := pipelineStage(t, removeInteraction(codecTime, keepValue)).Lookup("$set").Document()
		if got := set.Lookup(stateField, "$bitOr").Array().Index(1).Int32(); got != int32(store.BitInteraction) {
			t.Fatalf("removal(keep value %v) $bitOr %d, want %d", keepValue, got, store.BitInteraction)
		}
	}
	set := pipelineStage(t, removeReply(bson.D{}, codecTime)).Lookup("$set").Document()
	if _, err := set.LookupErr(stateField); err == nil {
		t.Fatalf("reply removal %s raises %s", set, stateField)
	}
}
