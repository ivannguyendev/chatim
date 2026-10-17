package itest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type linkProjections struct {
	replies  domain.ReplyCount
	threads  []store.ReplyThread
	mentions []domain.Mention
}

func pendingCount(t *testing.T, st *mongostore.Store) int {
	t.Helper()
	n := 0
	for _, c := range store.AllStateCollections() {
		docs, _, err := st.EventStates().Pending(t.Context(), c, time.Unix(0, 0), time.Now().Add(time.Hour), store.PendingCursor{}, store.MaxPendingPage)
		if err != nil {
			t.Fatalf("Pending(%s): %v", c, err)
		}
		n += len(docs)
	}
	return n
}

func awaitSettled(t *testing.T, it *itInfra, cfg config.Config, st *mongostore.Store, room uint64) {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for {
		docs, timers, events := pendingCount(t, st), pendingTimers(t, it, cfg, room), pendingProjections(t, it, cfg)
		if docs == 0 && timers == 0 && events == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d docs pending, %d timers armed and %d events not projected after %v, want none", docs, timers, events, itLiveLimit)
		}
		time.Sleep(itActivityPoll)
	}
}

func pendingProjections(t *testing.T, it *itInfra, cfg config.Config) uint64 {
	t.Helper()
	c, err := it.js.Consumer(t.Context(), cfg.Stream.Name, projection.DurableName)
	if err != nil {
		t.Fatalf("projections consumer: %v", err)
	}
	info, err := c.Info(t.Context())
	if err != nil {
		t.Fatalf("projections consumer info: %v", err)
	}
	return info.NumPending + uint64(info.NumAckPending)
}

func readLinkProjections(t *testing.T, st *mongostore.Store, room, parent, mention uint64) linkProjections {
	t.Helper()
	msgs, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: parent}})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Find(parent %d) = %+v, %v", parent, msgs, err)
	}
	threads, err := st.ReplyThreads().List(t.Context(), room, store.ReplyThreadCursor{}, 10)
	if err != nil {
		t.Fatalf("reply threads: %v", err)
	}
	mentions, err := st.Mentions().MentionsOf(t.Context(), store.MsgKey{Room: room, Seq: mention})
	if err != nil {
		t.Fatalf("MentionsOf(%d): %v", mention, err)
	}
	return linkProjections{replies: msgs[0].Replies, threads: threads, mentions: mentions}
}

func assertLinksUnchanged(t *testing.T, before, after linkProjections) {
	t.Helper()
	if before.replies.N != 1 || len(before.threads) != 1 || len(before.mentions) != 1 {
		t.Fatalf("projections before the purge = %+v, want one reply counted, one reply thread and one mention", before)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("projections after resync = %+v,\nwant them unchanged %+v", after, before)
	}
}

func writeEveryKind(t *testing.T, client chatimv1.CoreServiceClient, st *mongostore.Store, roomID string) (parent, reply, mention uint64) {
	t.Helper()
	parent = sendAs(t, client, itUser, roomID, "resync-1", "parent")
	reply = sendLinked(t, client, "bob", &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "resync-2", Text: "reply", ReplyTo: &chatimv1.ReplyRef{Seq: parent}})
	bob := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: "bob"}}}
	mention = sendLinked(t, client, itUser, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "resync-3", Text: "@bob", Mentions: bob})
	key := store.MsgKey{Room: parseRoom(t, roomID), Seq: mention}
	awaitStored(t, "mention of bob", func() ([]domain.Mention, error) { return st.Mentions().MentionsOf(t.Context(), key) },
		func(m []domain.Mention) bool { return len(m) == 1 })
	steps := []struct {
		name, as string
		do       func(ctx context.Context) error
	}{
		{"EditMessage", itUser, func(ctx context.Context) error {
			_, err := client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: roomID, Seq: parent, Text: "parent, edited"})
			return err
		}},
		{"ReactMessage", "bob", func(ctx context.Context) error {
			_, err := client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: parent, Emoji: "👍"})
			return err
		}},
		{"PinMessage", itUser, func(ctx context.Context) error {
			_, err := client.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: roomID, Seq: mention})
			return err
		}},
		{"MarkRead", "bob", func(ctx context.Context) error {
			_, err := client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: roomID, Seq: mention})
			return err
		}},
		{"HideMessage", "bob", func(ctx context.Context) error {
			_, err := client.HideMessage(ctx, &chatimv1.HideMessageRequest{RoomId: roomID, Seq: parent})
			return err
		}},
	}
	for _, step := range steps {
		if _, err := retryingUnavailable(callerAs(t.Context(), step.as), func(ctx context.Context) (struct{}, error) { return struct{}{}, step.do(ctx) }); err != nil {
			t.Fatalf("%s as %s: %v", step.name, step.as, err)
		}
	}
	addMembersAs(t, client, itUser, roomID, "resync-carol", "carol")
	if _, err := client.ClearHistory(callerAs(t.Context(), "carol"), &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	return parent, reply, mention
}

func currentStateIDs(t *testing.T, st *mongostore.Store, room, parent, mention uint64) []string {
	t.Helper()
	r, err := st.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get(%d): %v", room, err)
	}
	ids := []string{
		pbconv.RoomCreatedEventID(room), pbconv.MemberCountEventID(room, r.MemberCountVer),
		pbconv.MessageEventID(room, 0, parent), pbconv.MessageEventID(room, 0, parent+1), pbconv.MessageEventID(room, 0, mention),
		pbconv.MessageChangeEventID(room, 0, parent, 1), pbconv.ReactionEventID(room, 0, parent, "bob", 1), pbconv.PinEventID(room, 1),
		pbconv.HiddenEventID(room, "bob", 0, parent),
	}
	for user, m := range storedMembers(t, st, room, itUser, "bob", "carol") {
		ids = append(ids, pbconv.MemberEventID(room, user, m.Ver))
		if m.ReadVer >= 1 {
			ids = append(ids, pbconv.ReadEventID(room, user, m.ReadVer))
		}
		if !m.ClearedAt.IsZero() {
			ids = append(ids, pbconv.ClearedEventID(room, user, m.ClearedAt))
		}
	}
	return ids
}
