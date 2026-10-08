package itest

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func itReactions(st *mongostore.Store) store.Reactions { return st.Reactions() }

func itPins(st *mongostore.Store) store.Pins { return st.Pins() }

func createRoomWith(t *testing.T, client chatimv1.CoreServiceClient, members []string) string {
	t.Helper()
	resp, err := client.CreateRoom(caller(t.Context()), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "reactions", Members: members,
	})
	if err != nil {
		t.Fatalf("CreateRoom(%d members): %v", len(members), err)
	}
	return resp.GetRoom().GetId()
}

func awaitLiveEvents(t *testing.T, live <-chan *nats.Msg, ids ...string) map[string]*chatimv1.Event {
	t.Helper()
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	got := make(map[string]*chatimv1.Event, len(ids))
	deadline := time.After(itLiveLimit)
	for len(got) < len(wanted) {
		select {
		case m := <-live:
			id := m.Header.Get(jetstream.MsgIDHeader)
			if !wanted[id] || got[id] != nil {
				continue
			}
			ev := &chatimv1.Event{}
			if err := proto.Unmarshal(m.Data, ev); err != nil {
				t.Fatalf("decode live event %s: %v", id, err)
			}
			got[id] = ev
		case <-deadline:
			t.Fatalf("live events %v did not all arrive within %v; got %v", ids, itLiveLimit, slices.Sorted(maps.Keys(got)))
		}
	}
	return got
}

func awaitStored[T any](t *testing.T, what string, read func() (T, error), done func(T) bool) T {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for {
		got, err := read()
		if err != nil {
			t.Fatalf("read %s: %v", what, err)
		}
		if done(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %+v after %v", what, got, itLiveLimit)
		}
		time.Sleep(itActivityPoll)
	}
}

func awaitCounts(t *testing.T, st *mongostore.Store, key store.MsgKey, want []domain.ReactionCount) domain.ReactionSummary {
	t.Helper()
	read := func() (domain.ReactionSummary, error) {
		got, err := st.Find(t.Context(), key.Room, []store.MsgKey{key})
		if err == nil && len(got) != 1 {
			err = fmt.Errorf("found %d messages for %+v", len(got), key)
		}
		if err != nil {
			return domain.ReactionSummary{}, err
		}
		return got[0].Reactions, nil
	}
	what := fmt.Sprintf("reactions of seq %d (want %+v)", key.Seq, want)
	return awaitStored(t, what, read, func(s domain.ReactionSummary) bool { return slices.Equal(s.Counts, want) })
}

func countsOf(emojis ...string) []domain.ReactionCount {
	by := map[string]uint32{}
	for _, e := range emojis {
		if e != "" {
			by[e]++
		}
	}
	out := make([]domain.ReactionCount, 0, len(by))
	for e, n := range by {
		out = append(out, domain.ReactionCount{Emoji: e, Count: n})
	}
	domain.SortReactionCounts(out)
	return out
}

func sameCounts(got *chatimv1.ReactionSummary, want []domain.ReactionCount) bool {
	return slices.EqualFunc(got.GetCounts(), want, func(g *chatimv1.ReactionCount, w domain.ReactionCount) bool {
		return g.GetEmoji() == w.Emoji && g.GetCount() == w.Count
	})
}

func pinnedSeqs(pins []*chatimv1.Pin) []uint64 {
	out := make([]uint64, len(pins))
	for i, p := range pins {
		out[i] = p.GetSeq()
	}
	return out
}
