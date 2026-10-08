package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itQuietWindow = 3 * time.Second

type liveWant struct {
	class, kind string
}

func awaitExactlyOnce(t *testing.T, live <-chan *nats.Msg, cfg config.Config, roomID string, want map[string]liveWant) {
	t.Helper()
	seen := map[string]int{}
	deadline := time.After(itLiveLimit)
	var quiet <-chan time.Time
	for {
		select {
		case m := <-live:
			if strings.Split(m.Subject, ".")[3] != roomID {
				continue
			}
			id := m.Header.Get(jetstream.MsgIDHeader)
			w, ok := want[id]
			if !ok || seen[id] > 0 {
				t.Fatalf("live event %s on %s, want each of %v exactly once", id, m.Subject, slices.Sorted(maps.Keys(want)))
			}
			if subject := liveSubject(cfg, w.class, roomID, w.kind); m.Subject != subject {
				t.Fatalf("event %s on %s, want %s", id, m.Subject, subject)
			}
			seen[id]++
			if len(seen) == len(want) {
				quiet = time.After(itQuietWindow)
			}
		case <-quiet:
			return
		case <-deadline:
			if quiet == nil {
				t.Fatalf("live events %v arrived, want %v within %v", seen, slices.Sorted(maps.Keys(want)), itLiveLimit)
			}
		}
	}
}

func TestRealInfraEachChangeGoesToTheSubjectOfItsData(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	live := subscribeLive(t, it, core.cfg, "*")
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	awaitExactlyOnce(t, live, core.cfg, roomID, map[string]liveWant{
		pbconv.RoomCreatedEventID(room):       {"room", "room_created"},
		pbconv.MemberEventID(room, itUser, 1): {"member", "member_added"},
		pbconv.MemberEventID(room, "bob", 1):  {"member", "member_added"},
		pbconv.MemberCountEventID(room, 1):    {"room", "member_count_changed"},
	})

	seq := sendAs(t, client, itUser, roomID, "subject-1", "hello")
	added := addMembersAs(t, client, itUser, roomID, "subject-carol", "carol")
	read, err := client.MarkRead(callerAs(t.Context(), "bob"), &chatimv1.MarkReadRequest{RoomId: roomID, Seq: seq})
	if err != nil || len(added) != 1 || read.GetReadSeq() != seq {
		t.Fatalf("added %v, MarkRead = %v, %v; want carol added and bob's read position at %d", added, read, err, seq)
	}
	awaitExactlyOnce(t, live, core.cfg, roomID, map[string]liveWant{
		pbconv.MessageEventID(room, 0, seq):                    {"message", "msg_created"},
		pbconv.MemberEventID(room, "carol", added[0].GetVer()): {"member", "member_added"},
		pbconv.MemberCountEventID(room, 2):                     {"room", "member_count_changed"},
		pbconv.ReadEventID(room, "bob", read.GetReadVer()):     {"member", "read_updated"},
	})
}

func TestRealInfraMemberCountFollowsEachCommand(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	st := itStore(it, core)
	if r := assertCountMatchesDocs(t, st, room, 2); r.MemberCountVer != 1 {
		t.Fatalf("member_count_ver after create = %d, want 1", r.MemberCountVer)
	}

	if added := addMembersAs(t, client, itUser, roomID, "count-add", "bob", "carol", "dave"); len(added) != 2 {
		t.Fatalf("added %v, want carol and dave only", added)
	}
	after := assertCountMatchesDocs(t, st, room, 4)
	ev := awaitLiveEvent(t, live, pbconv.MemberCountEventID(room, after.MemberCountVer))
	if c := ev.GetMemberCountChanged(); after.MemberCountVer != 2 || c.GetMemberCount() != 4 || c.GetMemberCountVer() != 2 {
		t.Fatalf("after adding: stored ver %d, event %v; want 4 members at ver 2", after.MemberCountVer, ev)
	}

	removeAs(t, client, itUser, roomID, "carol")
	after = assertCountMatchesDocs(t, st, room, 3)
	ev = awaitLiveEvent(t, live, pbconv.MemberCountEventID(room, after.MemberCountVer))
	if c := ev.GetMemberCountChanged(); after.MemberCountVer != 3 || c.GetMemberCount() != 3 || c.GetMemberCountVer() != 3 {
		t.Fatalf("after removing: stored ver %d, event %v; want 3 members at ver 3", after.MemberCountVer, ev)
	}
}

func TestRealInfraHidingAMessageAgainPublishesNoEvent(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	published := make(chan *nats.Msg, 64)
	sub, err := it.nc.ChanSubscribe(core.cfg.Stream.SubjectRoot+"."+itTenant+".member."+roomID+".message_hidden", published)
	if err != nil {
		t.Fatalf("subscribe raw member events: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	seq := sendAs(t, client, itUser, roomID, "rehide-1", "hide me twice")

	hide := &chatimv1.HideMessageRequest{RoomId: roomID, Seq: seq}
	for attempt := range 2 {
		if _, err := client.HideMessage(callerAs(t.Context(), "bob"), hide); err != nil {
			t.Fatalf("HideMessage attempt %d: %v", attempt+1, err)
		}
	}
	id := pbconv.HiddenEventID(room, "bob", 0, seq)
	publishes := 0
	deadline := time.After(itLiveLimit)
	var quiet <-chan time.Time
	for {
		select {
		case m := <-published:
			if m.Header.Get(jetstream.MsgIDHeader) != id {
				t.Fatalf("raw publish %s, want only %s", m.Header.Get(jetstream.MsgIDHeader), id)
			}
			if publishes++; publishes == 2 {
				quiet = time.After(core.cfg.EffectDelay + time.Second)
			}
		case <-quiet:
			if publishes != 2 {
				t.Fatalf("message_hidden published %d times, want the fast path and the worker only", publishes)
			}
			return
		case <-deadline:
			t.Fatalf("message_hidden published %d times within %v, want 2", publishes, itLiveLimit)
		}
	}
}
