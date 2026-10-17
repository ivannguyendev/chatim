package mongostore

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

const replyThreadsIndex = "room_id_1_last_reply_at_-1__id_-1"

func TestMongoReplyThreadsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunReplyThreads(t, func(t *testing.T) store.ReplyThreads {
		s, _ := itStore(t, client)
		return s.ReplyThreads()
	})
}

func TestReplyThreadTouchAgainLeavesTheDocUnchanged(t *testing.T) {
	s, db := itStore(t, itClient(t))
	parent := store.MsgKey{Room: itRoom, Seq: 40}
	touch := func(seq uint64, at int) bson.Raw {
		t.Helper()
		if err := s.ReplyThreads().Touch(t.Context(), parent, "acme", seq, codecTime.Add(time.Duration(at)*time.Second)); err != nil {
			t.Fatalf("Touch(%d): %v", seq, err)
		}
		raw, err := db.Collection(replyThreadsCollection).FindOne(t.Context(), idIs(msgID(parent))).Raw()
		if err != nil {
			t.Fatalf("FindOne: %v", err)
		}
		return raw
	}
	first := touch(60, 30)
	if got, want := fieldNames(t, first), []string{"_id", "room_id", "tenant", "last_reply_at", "last_reply_seq", "updated_at"}; !slices.Equal(got, want) {
		t.Fatalf("reply thread fields = %v, want %v", got, want)
	}
	for _, again := range []bson.Raw{touch(60, 30), touch(57, 9)} {
		if !bytes.Equal(again, first) {
			t.Fatalf("an equal or older touch changed the doc: %s -> %s", first, again)
		}
	}
}

func TestReplyThreadsListUsesTheRecentIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	for seq := uint64(1); seq <= 3; seq++ {
		if err := s.ReplyThreads().Touch(t.Context(), store.MsgKey{Room: itRoom, Seq: seq}, "acme", seq+10, codecTime); err != nil {
			t.Fatalf("Touch: %v", err)
		}
	}
	for name, before := range map[string]store.ReplyThreadCursor{"first page": {}, "after a cursor": {At: codecTime, Seq: 3}} {
		find := bson.D{
			{Key: "find", Value: replyThreadsCollection}, {Key: "filter", Value: replyThreadFilter(int64(itRoom), itRoom, before)},
			{Key: "sort", Value: bson.D{{Key: "last_reply_at", Value: -1}, {Key: "_id", Value: -1}}}, {Key: "limit", Value: 50},
			{Key: "hint", Value: replyThreadsByRecent},
		}
		stages, indexes := winningPlan(t, db, find)
		if !slices.Contains(indexes, replyThreadsIndex) || slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") {
			t.Fatalf("%s plan stages %v on %v, want %s without SORT", name, stages, indexes, replyThreadsIndex)
		}
	}
}

func TestBootstrapClustersReplyThreads(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, replyThreadsCollection)
	if got := indexKeys(t, db.Collection(replyThreadsCollection)); !hasIndex(got, "room_id:1,last_reply_at:-1,_id:-1", false) {
		t.Fatalf("reply_threads indexes = %v, want non-unique room_id:1,last_reply_at:-1,_id:-1", got)
	}
}
