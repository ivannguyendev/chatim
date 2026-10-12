package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	mentionTargetIndex = "tenant_1_target_1_state_1_created_at_-1_message_key_-1"
	mentionRoomIndex   = "room_id_1_kind_1_created_at_-1_message_key_-1"
)

func assertMergedScan(t *testing.T, name string, stages, indexes []string, index string) {
	t.Helper()
	scans := 0
	for _, st := range stages {
		if st == "IXSCAN" {
			scans++
		}
	}
	t.Logf("%s: winning plan %v over %d index scans", name, stages[:min(len(stages), 4)], scans)
	if slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") || !slices.Contains(indexes, index) {
		t.Fatalf("%s: winning plan stages %v on %v, want merged %s scans without a blocking SORT", name, stages, indexes, index)
	}
	for _, ix := range indexes {
		if ix != index {
			t.Fatalf("%s: winning plan uses %s, want only %s", name, ix, index)
		}
	}
}

func explainMentions(filter bson.D, limit int) bson.D {
	return bson.D{{Key: "find", Value: mentionsCollection}, {Key: "filter", Value: filter}, {Key: "sort", Value: mentionListSort()}, {Key: "limit", Value: limit}}
}

func TestListByTargetMergesStateRangesWithoutASort(t *testing.T) {
	s, db := itStore(t, itClient(t))
	minh := domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	for i := range 6 {
		set := store.MentionSet{Key: store.MsgKey{Room: itRoom, Seq: uint64(i + 1)}, Tenant: "acme", Sender: "alice", Targets: []domain.MentionTarget{minh}, CreatedAt: codecTime.Add(time.Duration(i) * time.Second), At: codecTime}
		if err := s.Mentions().ApplyMentions(t.Context(), set); err != nil {
			t.Fatalf("ApplyMentions: %v", err)
		}
	}
	before := store.MentionCursor{At: codecTime.Add(4 * time.Second), Key: store.MsgKey{Room: itRoom, Seq: 5}}
	for name, q := range map[string]store.TargetMentionQuery{
		"mark":        {Tenant: "acme", Target: minh.Name(0), Limit: 20},
		"hide":        {Tenant: "acme", Target: minh.Name(0), UnreadOnly: true, Limit: 20},
		"after a key": {Tenant: "acme", Target: minh.Name(0), Before: before, Limit: 20},
	} {
		stages, indexes := winningPlan(t, db, explainMentions(targetMentionFilter(q), q.Limit))
		assertMergedScan(t, name, stages, indexes, mentionTargetIndex)
	}
}

func TestListSharedMergesAFullRoomBatchWithoutASort(t *testing.T) {
	s, db := itStore(t, itClient(t))
	all := domain.MentionTarget{Kind: domain.MentionAll}
	ops := domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}
	rooms := make([]store.SharedRoom, store.SharedMentionBatch)
	for i := range rooms {
		room := itRoom + uint64(i)
		rooms[i] = store.SharedRoom{Room: room, Since: codecTime.Add(-time.Hour)}
		set := store.MentionSet{Key: store.MsgKey{Room: room, Seq: 1}, Tenant: "acme", Sender: "alice", Targets: []domain.MentionTarget{all, ops}, CreatedAt: codecTime.Add(time.Duration(i) * time.Millisecond), At: codecTime}
		if err := s.Mentions().ApplyMentions(t.Context(), set); err != nil {
			t.Fatalf("ApplyMentions: %v", err)
		}
	}
	before := store.MentionCursor{At: codecTime.Add(100 * time.Millisecond), Key: store.MsgKey{Room: itRoom + 100, Seq: 1}}
	for name, q := range map[string]store.SharedMentionQuery{
		"all and groups": {Tenant: "acme", Rooms: rooms, Groups: []string{"ops", "hcm"}, Limit: 20},
		"group since":    {Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, GroupSince: codecTime, Limit: 20},
		"all only":       {Tenant: "acme", Rooms: rooms, Limit: 20},
		"after a key":    {Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, Before: before, Limit: 20},
	} {
		filter, err := sharedMentionFilter(q)
		if err != nil {
			t.Fatalf("sharedMentionFilter: %v", err)
		}
		stages, indexes := winningPlan(t, db, explainMentions(filter, q.Limit))
		assertMergedScan(t, name, stages, indexes, mentionRoomIndex)
	}
	got, err := s.Mentions().ListShared(t.Context(), store.SharedMentionQuery{Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, Before: before, Limit: 3})
	if err != nil {
		t.Fatalf("ListShared: %v", err)
	}
	var seen []uint64
	for _, m := range got {
		seen = append(seen, m.Key.Room-itRoom)
	}
	if want := []uint64{99, 99, 98}; !slices.Equal(seen, want) {
		t.Fatalf("ListShared rooms = %v, want %v", seen, want)
	}
}
