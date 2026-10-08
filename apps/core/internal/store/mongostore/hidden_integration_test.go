package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestHiddenDocumentLayoutKeepsTheFirstHideTime(t *testing.T) {
	s, db := itStore(t, itClient(t))
	key := store.MsgKey{Room: itRoom, Thread: 3, Seq: 9}
	for i := range 2 {
		if fresh, err := s.Hidden().Hide(t.Context(), "bob", key, codecTime.Add(time.Duration(i)*time.Hour)); err != nil || fresh != (i == 0) {
			t.Fatalf("Hide #%d = %v, %v; want newly hidden %v", i+1, fresh, err, i == 0)
		}
	}
	raws, err := db.Collection(hiddenCollection).Find(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	var docs []bson.Raw
	if err := raws.All(t.Context(), &docs); err != nil || len(docs) != 1 {
		t.Fatalf("hidden docs = %d, %v; want one", len(docs), err)
	}
	got := fieldNames(t, docs[0])
	slices.Sort(got)
	if want := []string{"_id", "created_at", "room_id", "seq", "thread_root", "user_id"}; !slices.Equal(got, want) {
		t.Fatalf("stored fields = %v, want %v", got, want)
	}
	if at := docs[0].Lookup("created_at").Time(); !at.Equal(codecTime) {
		t.Fatalf("created_at = %v, want the first hide %v", at, codecTime)
	}
}
