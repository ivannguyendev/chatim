package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoMentionsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMentions(t, func(t *testing.T) store.Mentions {
		s, _ := itStore(t, client)
		return s.Mentions()
	})
}

func TestBootstrapCreatesMentions(t *testing.T) {
	_, db := itStore(t, itClient(t))
	assertClusteredLayout(t, db, mentionsCollection)
	got := indexKeys(t, db.Collection(mentionsCollection))
	for _, pattern := range []string{"tenant:1,target:1,state:1,created_at:-1", "message_key:1"} {
		if !hasIndex(got, pattern, false) {
			t.Fatalf("mentions indexes = %v, want non-unique %s", got, pattern)
		}
	}
}

func TestMentionDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	key := store.MsgKey{Room: itRoom, Seq: 4}
	all := domain.MentionTarget{Kind: domain.MentionAll}
	set := store.MentionSet{Key: key, Tenant: "acme", Sender: "alice", Targets: []domain.MentionTarget{{Kind: domain.MentionGroup, ID: "ops"}, all}, CreatedAt: codecTime, At: codecTime}
	if err := s.Mentions().ApplyMentions(t.Context(), set); err != nil {
		t.Fatalf("ApplyMentions: %v", err)
	}
	want := []string{"_id", "created_at", "message_key", "message_ver", "room_id", "sender_id", "state", "target", "tenant", "updated_at"}
	for target, id := range map[string][]byte{
		"group:ops":      keys.Mention(keys.Msg(itRoom, 0, 4), keys.MentionGroupKind, "ops"),
		all.Name(itRoom): keys.Mention(keys.Msg(itRoom, 0, 4), keys.MentionAllKind, ""),
	} {
		raw, err := db.Collection(mentionsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: id}}).Raw()
		if err != nil {
			t.Fatalf("FindOne %s: %v", target, err)
		}
		if got := slices.Sorted(slices.Values(fieldNames(t, raw))); !slices.Equal(got, want) {
			t.Fatalf("%s stored fields = %v, want %v", target, got, want)
		}
		if got := raw.Lookup("target").StringValue(); got != target {
			t.Fatalf("stored target = %q, want %q", got, target)
		}
	}
}

func TestAnOlderMentionWriteLosingARaceSettles(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	minh := domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	key := store.MsgKey{Room: itRoom, Seq: 5}
	newer := store.MentionSet{Key: key, Tenant: "acme", Sender: "alice", Ver: 3, Targets: []domain.MentionTarget{minh}, CreatedAt: codecTime, At: codecTime}
	if err := s.Mentions().ApplyMentions(t.Context(), newer); err != nil {
		t.Fatalf("ApplyMentions v3: %v", err)
	}
	older := newer
	older.Ver, older.At = 1, codecTime.Add(time.Minute)
	if err := s.Mentions().write(t.Context(), older, int64(itRoom), store.MentionPlan{Live: []domain.MentionTarget{minh}}); err != nil {
		t.Fatalf("older write that lost the race = %v, want nil", err)
	}
	got, err := s.Mentions().MentionsOf(t.Context(), key)
	if err != nil || len(got) != 1 || got[0].Ver != 3 || !got[0].Live || !got[0].UpdatedAt.Equal(codecTime) {
		t.Fatalf("MentionsOf = %+v, %v; want the v3 doc unchanged", got, err)
	}
}
