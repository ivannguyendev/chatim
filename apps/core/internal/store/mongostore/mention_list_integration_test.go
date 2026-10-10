package mongostore

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const mentionListIndex = "tenant_1_target_1_state_1_created_at_-1_message_key_-1"

func TestListMentionsMergesIndexRangesWithoutASort(t *testing.T) {
	s, db := itStore(t, itClient(t))
	var wants []store.MentionWant
	for i := range 6 {
		target := domain.MentionTarget{Kind: domain.MentionGroup, ID: fmt.Sprintf("g%d", i)}
		set := store.MentionSet{Key: store.MsgKey{Room: itRoom, Seq: uint64(i + 1)}, Tenant: "acme", Sender: "alice", Targets: []domain.MentionTarget{target}, CreatedAt: codecTime.Add(time.Duration(i) * time.Second), At: codecTime}
		if err := s.Mentions().ApplyMentions(t.Context(), set); err != nil {
			t.Fatalf("ApplyMentions: %v", err)
		}
		w := store.MentionWant{Target: target.Name(itRoom)}
		if i%3 == 0 {
			w.Since = codecTime.Add(2 * time.Second)
		}
		wants = append(wants, w)
	}
	for name, q := range map[string]store.MentionQuery{
		"first page":  {Tenant: "acme", Targets: wants, Limit: 50},
		"after a key": {Tenant: "acme", Targets: wants, Limit: 50, Before: store.MentionCursor{At: codecTime.Add(4 * time.Second), Key: store.MsgKey{Room: itRoom, Seq: 5}}},
		"plain only":  {Tenant: "acme", Targets: wants[1:3], Limit: 50},
	} {
		find := bson.D{{Key: "find", Value: mentionsCollection}, {Key: "filter", Value: mentionListFilter(q)}, {Key: "sort", Value: mentionListSort()}, {Key: "limit", Value: q.Limit}}
		stages, indexes := winningPlan(t, db, find)
		t.Logf("%s: winning plan stages %v on %v", name, stages, indexes)
		if slices.Contains(stages, "SORT") || slices.Contains(stages, "COLLSCAN") || !slices.Contains(indexes, mentionListIndex) {
			t.Fatalf("%s: winning plan stages %v on %v, want merged %s scans without a blocking SORT", name, stages, indexes, mentionListIndex)
		}
	}
	got, err := s.Mentions().List(t.Context(), store.MentionQuery{Tenant: "acme", Targets: wants, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var seqs []uint64
	for _, m := range got {
		seqs = append(seqs, m.Key.Seq)
	}
	if want := []uint64{6, 5, 4, 3, 2}; !slices.Equal(seqs, want) {
		t.Fatalf("List seqs = %v, want %v (g0 is older than its since)", seqs, want)
	}
}
