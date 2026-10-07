package main

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itReactors = 24

var itEmojis = []string{"👍", "😂", "❤️"}

func itReactorNames() []string {
	users := []string{itUser}
	for i := 1; i < itReactors; i++ {
		users = append(users, "r"+strconv.Itoa(i))
	}
	return users
}

func reactAll(t *testing.T, client chatimv1.CoreServiceClient, roomID string, seq uint64, users []string, change uint32, emojiOf func(int) string) []domain.ReactionCount {
	t.Helper()
	emojis := make([]string, len(users))
	errs := make([]error, len(users))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, user := range users {
		emojis[i] = emojiOf(i)
		req := &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: emojis[i]}
		wg.Go(func() {
			<-start
			resp, err := client.ReactMessage(callerAs(t.Context(), user), req)
			if err == nil && resp.GetChange() != change {
				err = fmt.Errorf("change %d, want %d", resp.GetChange(), change)
			}
			errs[i] = err
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s reacts %q: %v", users[i], emojis[i], err)
		}
	}
	return countsOf(emojis...)
}

func TestRealInfraWorkersPublishReactionChangesWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "react-a", "reacted outside the core")
	core.awaitTerm(t)

	st := itStore(it, core)
	key := store.MsgKey{Room: room, Seq: seq}
	steps := []struct {
		n           uint32
		emoji, prev string
	}{{1, "👍", ""}, {2, "😂", "👍"}, {3, "", "😂"}}
	for _, step := range steps {
		n := step.n
		at := time.Now().UTC().Truncate(time.Millisecond)
		var changed bool
		var err error
		if step.emoji == "" {
			_, changed, err = itReactions(st).Remove(t.Context(), key, "bob", at)
		} else {
			_, changed, err = itReactions(st).Set(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: itTenant, User: "bob", Emoji: step.emoji, At: at})
		}
		if err != nil || !changed {
			t.Fatalf("change %d outside the core = %v, %v; want a change", n, changed, err)
		}
		reactionID, countsID := pbconv.ReactionEventID(room, 0, seq, "bob", n), pbconv.ReactionCountsEventID(room, 0, seq, uint64(n))
		evs := awaitLiveEvents(t, live, reactionID, countsID)
		if r := evs[reactionID].GetReactionChanged(); r.GetUser() != "bob" || r.GetEmoji() != step.emoji || r.GetPreviousEmoji() != step.prev || r.GetChange() != n {
			t.Fatalf("reaction_changed %d = %v, want %q after %q by bob", n, r, step.emoji, step.prev)
		}
		c := evs[countsID].GetCountsChanged()
		if c.GetCounter() != pbconv.ReactionsCounter || c.GetReactions().GetVer() != uint64(n) || !sameCounts(c.GetReactions(), countsOf(step.emoji)) {
			t.Fatalf("counts_changed %d = %v, want the counts of %q at version %d", n, c, step.emoji, n)
		}
	}
	got, err := st.Find(t.Context(), room, []store.MsgKey{key})
	if err != nil || len(got) != 1 || len(got[0].Reactions.Counts) != 0 || got[0].Reactions.Version != 3 {
		t.Fatalf("summary after the removal = %+v, %v; want no counts at version 3", got, err)
	}
}

func TestRealInfraConcurrentReactionsConvergeToExactCounts(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	users := itReactorNames()
	roomID := createRoomWith(t, client, users)
	seq := sendAs(t, client, itUser, roomID, "react-c", "react to me")
	key := store.MsgKey{Room: parseRoom(t, roomID), Seq: seq}
	st := itStore(it, core)

	first := reactAll(t, client, roomID, seq, users, 1, func(i int) string { return itEmojis[i%len(itEmojis)] })
	before := awaitCounts(t, st, key, first)
	second := reactAll(t, client, roomID, seq, users, 2, func(i int) string {
		if i%4 == 0 {
			return ""
		}
		return itEmojis[(i+1)%len(itEmojis)]
	})
	after := awaitCounts(t, st, key, second)
	if before.Version == 0 || after.Version <= before.Version {
		t.Fatalf("rx.v went from %d to %d, want it above zero and rising", before.Version, after.Version)
	}
	facts, err := itReactions(st).Count(t.Context(), key, nil)
	if err != nil || !slices.Equal(facts, second) {
		t.Fatalf("Count = %+v, %v; want %+v", facts, err, second)
	}
	m := historyAs(t, client, "r1", roomID)[seq]
	if !sameCounts(m.GetReactions(), second) || m.GetReactions().GetVer() < after.Version {
		t.Fatalf("history shows %v, want %+v at version %d or later", m.GetReactions(), second, after.Version)
	}
}
