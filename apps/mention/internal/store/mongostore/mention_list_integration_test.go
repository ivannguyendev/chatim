package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
)

const (
	itRoom             uint64 = 9_100_000_001
	mentionTargetIndex        = "tenant_1_target_1_state_1_created_at_-1_message_key_-1"
	mentionRoomIndex          = "room_id_1_kind_1_created_at_-1_message_key_-1"
)

var (
	itTime  = time.UnixMilli(1_700_000_000_000).UTC()
	itMinh  = domain.Target{Kind: domain.TargetUser, ID: "minh"}
	itOps   = domain.Target{Kind: domain.TargetGroup, ID: "ops"}
	itAll   = domain.Target{Kind: domain.TargetAll}
	itPlace = func(room, seq uint64, at time.Time, targets ...domain.Target) domain.Plan {
		live := make([]domain.Target, len(targets))
		for i, x := range targets {
			live[i], live[i].At = x, at
		}
		return domain.Plan{Key: domain.MessageKey{Room: room, Seq: seq}, Tenant: "acme", Sender: "alice", Live: live}
	}
)

func mustApplyAll(t *testing.T, m *Mentions, plans ...domain.Plan) {
	t.Helper()
	for _, p := range plans {
		if err := m.Apply(t.Context(), p); err != nil {
			t.Fatalf("Apply(%v): %v", p.Key, err)
		}
	}
}

func TestListByTargetScansTheTargetIndexWithoutASort(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	m := NewMentions(db)
	for i := range 6 {
		mustApplyAll(t, m, itPlace(itRoom, uint64(i+1), itTime.Add(time.Duration(i)*time.Second), itMinh))
	}
	before := store.MentionCursor{At: itTime.Add(4 * time.Second), Key: domain.MessageKey{Room: itRoom, Seq: 5}}
	for name, q := range map[string]store.TargetMentionQuery{
		"first page":  {Tenant: "acme", Target: itMinh.Name(0), Limit: 20},
		"after a key": {Tenant: "acme", Target: itMinh.Name(0), Before: before, Limit: 20},
	} {
		assertOnlyIndex(t, name, explain(t, db, findCmd(mentionsCollection, targetMentionFilter(q), mentionListSort(), q.Limit)), mentionTargetIndex)
	}
}

func TestListSharedMergesAFullRoomBatchWithoutASort(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	m := NewMentions(db)
	rooms := make([]store.SharedRoom, store.SharedMentionBatch)
	for i := range rooms {
		room := itRoom + uint64(i)
		rooms[i] = store.SharedRoom{Room: room, Since: itTime.Add(-time.Hour)}
		mustApplyAll(t, m, itPlace(room, 1, itTime.Add(time.Duration(i)*time.Millisecond), itAll, itOps))
	}
	before := store.MentionCursor{At: itTime.Add(100 * time.Millisecond), Key: domain.MessageKey{Room: itRoom + 100, Seq: 1}}
	for name, q := range map[string]store.SharedMentionQuery{
		"all and groups": {Tenant: "acme", Rooms: rooms, Groups: []string{"ops", "hcm"}, Limit: 20},
		"group since":    {Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, GroupSince: itTime, Limit: 20},
		"all only":       {Tenant: "acme", Rooms: rooms, Limit: 20},
		"after a key":    {Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, Before: before, Limit: 20},
	} {
		filter, err := sharedMentionFilter(q)
		if err != nil {
			t.Fatalf("sharedMentionFilter: %v", err)
		}
		assertOnlyIndex(t, name, explain(t, db, findCmd(mentionsCollection, filter, mentionListSort(), q.Limit)), mentionRoomIndex)
	}
	got, err := m.ListShared(t.Context(), store.SharedMentionQuery{Tenant: "acme", Rooms: rooms, Groups: []string{"ops"}, Before: before, Limit: 3})
	if err != nil {
		t.Fatalf("ListShared: %v", err)
	}
	var seen []uint64
	for _, x := range got {
		seen = append(seen, x.Key.Room-itRoom)
	}
	if want := []uint64{99, 99, 98}; !slices.Equal(seen, want) {
		t.Fatalf("ListShared rooms = %v, want %v", seen, want)
	}
}

func TestApplyWritesEachTargetByItsID(t *testing.T) {
	db := itBootstrapped(t, itClient(t))
	m := NewMentions(db)
	for seq := range uint64(50) {
		mustApplyAll(t, m, itPlace(itRoom, seq+1, itTime, itMinh, itOps, itAll))
	}
	plan := itPlace(itRoom, 20, itTime, itMinh, itOps, itAll, domain.Target{Kind: domain.TargetUser, ID: "lan"})
	plan.Ver, plan.Retired = 1, []domain.Target{{Kind: domain.TargetGroup, ID: "hcm"}}
	models, err := mentionModels(plan, itTime)
	if err != nil || len(models) != 5 {
		t.Fatalf("mentionModels = %d models, %v; want 5", len(models), err)
	}
	for _, wm := range models {
		u := wm.(*mongo.UpdateOneModel)
		cmd := bson.D{{Key: "update", Value: mentionsCollection}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: u.Filter}, {Key: "u", Value: u.Update}, {Key: "upsert", Value: true}}}}}
		p := explain(t, db, cmd)
		t.Logf("update: stages %v, examined %d", p.stages, p.examined)
		if slices.Contains(p.stages, "COLLSCAN") || len(p.indexes) > 0 || p.examined > 1 {
			t.Fatalf("update %v: stages %v on %v examined %d, want one clustered _id lookup", u.Filter, p.stages, p.indexes, p.examined)
		}
	}
}
