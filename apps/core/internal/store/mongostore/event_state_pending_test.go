package mongostore

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var pendingBase = time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)

func TestPendingFilterWithoutCursorIsOneIndexedRange(t *testing.T) {
	from, before := pendingBase.Add(1500*time.Microsecond), pendingBase.Add(time.Second)
	got, ok, err := pendingFilter(store.StateMessages, from, before, store.PendingCursor{})
	want := bson.D{{Key: stateAtField, Value: bson.D{{Key: "$gte", Value: pendingBase.Add(time.Millisecond)}, {Key: "$lt", Value: before}}}, stateExists}
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingFilter = %v, %v, %v; want %v", got, ok, err, want)
	}
}

func TestPendingFilterAfterCursorIsTieThenLaterBranch(t *testing.T) {
	at := pendingBase.Add(2 * time.Second)
	key := keys.Msg(9, 0, 4)
	got, ok, err := pendingFilter(store.StateMessages, pendingBase, pendingBase.Add(time.Minute), store.PendingCursor{At: at, Key: key})
	want := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: stateAtField, Value: at}, {Key: "_id", Value: bson.D{{Key: "$gt", Value: key}}}, stateExists},
		bson.D{{Key: stateAtField, Value: bson.D{{Key: "$gt", Value: at}, {Key: "$lt", Value: pendingBase.Add(time.Minute)}}}, stateExists},
	}}}
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingFilter = %v, %v, %v; want %v", got, ok, err, want)
	}
	early, ok, err := pendingFilter(store.StateMessages, at, pendingBase.Add(time.Minute), store.PendingCursor{At: pendingBase, Key: key})
	if err != nil || !ok || len(early) != 2 || early[0].Key != stateAtField {
		t.Fatalf("pendingFilter with a cursor before from = %v, %v, %v; want the plain range", early, ok, err)
	}
	if _, ok, err := pendingFilter(store.StateMessages, pendingBase, at, store.PendingCursor{At: at, Key: key}); ok || err != nil {
		t.Fatalf("pendingFilter with a cursor at before = %v, %v; want nothing to read", ok, err)
	}
}

func TestPendingCursorKeyOfRoomsAndHidden(t *testing.T) {
	id, err := cursorID(store.StateRooms, binary.BigEndian.AppendUint64(nil, 77))
	if err != nil || id != int64(77) {
		t.Fatalf("rooms cursor id = %v, %v; want 77", id, err)
	}
	oid := bson.NewObjectID()
	if id, err := cursorID(store.StateHidden, oid[:]); err != nil || id != oid {
		t.Fatalf("hidden cursor id = %v, %v; want %v", id, err, oid)
	}
	for c, key := range map[store.StateCollection][]byte{store.StateRooms: {1, 2}, store.StateHidden: {1}} {
		if _, err := cursorID(c, key); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("cursorID(%s, %x) error = %v, want invalid argument", c, key, err)
		}
	}
}

func rawID(t *testing.T, v any) bson.RawValue {
	t.Helper()
	typ, data, err := bson.MarshalValue(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return bson.RawValue{Type: typ, Value: data}
}

func TestDecodePendingReadsTheKeyOfEveryCollection(t *testing.T) {
	msg := keys.Msg(9, 7, 4)
	oid := bson.NewObjectID()
	tests := []struct {
		c    store.StateCollection
		row  pendingRow
		want store.PendingDoc
		key  []byte
	}{
		{store.StateMessages, pendingRow{ID: rawID(t, msg)}, store.PendingDoc{Room: 9, Thread: 7, Seq: 4}, msg},
		{store.StateEdits, pendingRow{ID: rawID(t, keys.Edit(9, 7, 4, 3))}, store.PendingDoc{Room: 9, Thread: 7, Seq: 4, Ver: 3}, keys.Edit(9, 7, 4, 3)},
		{store.StateInteractions, pendingRow{ID: rawID(t, keys.InteractionUser(msg, keys.BookmarkKind, "bob"))}, store.PendingDoc{Room: 9, Thread: 7, Seq: 4, User: "bob", Kind: keys.BookmarkKind}, keys.InteractionUser(msg, keys.BookmarkKind, "bob")},
		{store.StatePins, pendingRow{ID: rawID(t, keys.Pin(9, 5))}, store.PendingDoc{Room: 9, Seq: 5}, keys.Pin(9, 5)},
		{store.StateMembers, pendingRow{ID: rawID(t, keys.Member(9, "bob"))}, store.PendingDoc{Room: 9, User: "bob"}, keys.Member(9, "bob")},
		{store.StateHidden, pendingRow{ID: rawID(t, oid), User: "bob", Room: 9, Thread: 7, Seq: 4}, store.PendingDoc{Room: 9, Thread: 7, Seq: 4, User: "bob"}, oid[:]},
		{store.StateRooms, pendingRow{ID: rawID(t, int64(9))}, store.PendingDoc{Room: 9}, binary.BigEndian.AppendUint64(nil, 9)},
	}
	for _, tt := range tests {
		tt.row.State, tt.row.StateAt = 3, pendingBase
		tt.want.Collection, tt.want.State, tt.want.StateAt = tt.c, 3, pendingBase
		got, key, err := decodePending(tt.c, tt.row)
		if err != nil || !reflect.DeepEqual(got, tt.want) || !reflect.DeepEqual(key, tt.key) {
			t.Fatalf("decodePending(%s) = %+v, %x, %v; want %+v, %x", tt.c, got, key, err, tt.want, tt.key)
		}
	}
}

func TestDecodePendingRejectsCorruptRows(t *testing.T) {
	msg := keys.Msg(9, 0, 4)
	for name, tt := range map[string]struct {
		c   store.StateCollection
		row pendingRow
	}{
		"short message key": {store.StateMessages, pendingRow{ID: rawID(t, []byte{1, 2}), State: 1}},
		"reply interaction": {store.StateInteractions, pendingRow{ID: rawID(t, keys.InteractionReply(msg, 0, 5)), State: 1}},
		"room id string":    {store.StateRooms, pendingRow{ID: rawID(t, "9"), State: 1}},
		"no bits":           {store.StateMessages, pendingRow{ID: rawID(t, msg)}},
		"bits too wide":     {store.StateMessages, pendingRow{ID: rawID(t, msg), State: 1 << 40}},
	} {
		if _, _, err := decodePending(tt.c, tt.row); !errors.Is(err, errCorrupt) {
			t.Fatalf("%s: decodePending error = %v, want errCorrupt", name, err)
		}
	}
}
