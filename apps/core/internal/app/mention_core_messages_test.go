package app

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
)

func TestMentionCoreLooksUpTimesAndVersionsBeyondOneStoreBatch(t *testing.T) {
	c, _, msgs := newMentionCore(t)
	total := store.MaxMessageLookup + 5
	batch := make([]domain.Message, total)
	keys := make([]mentionapp.MessageKey, 0, total+1)
	for i := range batch {
		seq := uint64(i + 1)
		batch[i] = domain.Message{Room: mentionRoom, Seq: seq, Tenant: mentionTenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: mentionJoined.Add(time.Duration(seq) * time.Second), Version: uint32(seq % 3)}
		keys = append(keys, mentionapp.MessageKey{Room: mentionRoom, Seq: seq})
	}
	for _, r := range msgs.Insert(t.Context(), batch) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert: %+v", r)
		}
	}
	keys = append(keys, mentionapp.MessageKey{Room: mentionRoom, Seq: 9999})
	times, err := c.SentTimes(t.Context(), keys)
	if err != nil || len(times) != total || !times[keys[total-1]].Equal(batch[total-1].CreatedAt) {
		t.Fatalf("SentTimes = %d times, %v; want %d", len(times), err, total)
	}
	versions, err := c.MessageVersions(t.Context(), keys)
	if err != nil || len(versions) != total || versions[keys[1]] != 2 || versions[keys[2]] != 0 {
		t.Fatalf("MessageVersions = %d versions, %v; want %d with seq 2 at v2", len(versions), err, total)
	}
}

func TestMentionCoreConvertsVisibleMessagesOncePerKey(t *testing.T) {
	c, _, _ := newMentionCore(t)
	visible := c.visible.(*fakeVisible)
	shown := domain.Message{Room: mentionRoom, Thread: 4, Seq: 2, Tenant: mentionTenant, From: "bob", Kind: domain.KindText, Text: "hey", CreatedAt: mentionJoined}
	visible.got = map[store.MsgKey]domain.Message{store.KeyOf(shown): shown}
	keys := []mentionapp.MessageKey{{Room: mentionRoom, Thread: 4, Seq: 2}, {Room: mentionRoom, Seq: 1}, {Room: mentionRoom, Thread: 4, Seq: 2}}
	got, err := c.VisibleMessages(t.Context(), mentionTenant, "alice", keys)
	if err != nil || len(got) != 1 || got[0].GetText() != "hey" || got[0].GetThreadRoot() != 4 || got[0].GetSeq() != 2 {
		t.Fatalf("VisibleMessages = %v, %v; want the one visible message", got, err)
	}
	if len(visible.asked) != 3 || visible.asked[1] != (store.MsgKey{Room: mentionRoom, Seq: 1}) {
		t.Fatalf("asked %v, want every key converted", visible.asked)
	}
}
