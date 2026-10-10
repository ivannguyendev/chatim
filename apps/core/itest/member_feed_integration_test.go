package itest

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itMembersRepairedSeries = `chatim_core_counter_repaired_total{counter="members"}`

var errCoreDiedBeforeCount = errors.New("core died before the member count")

type countLost struct{ *mongostore.Store }

func (countLost) AddMemberCount(context.Context, uint64, int) (domain.MemberCount, error) {
	return domain.MemberCount{}, errCoreDiedBeforeCount
}

type droppedEvents struct{}

func (droppedEvents) Enqueue(uint64, []*chatimv1.Event) error { return nil }

type freshRequests struct{}

func (freshRequests) Begin(context.Context, dedupe.Key) (dedupe.RequestStatus, error) {
	return dedupe.RequestNew, nil
}

func (freshRequests) Finish(context.Context, dedupe.Key, dedupe.Record) {}

func (freshRequests) Cancel(context.Context, dedupe.Key) {}

type noForget struct{}

func (noForget) ForgetMembers(uint64) {}

func mutatorWithoutEvents(t *testing.T, it *itInfra, core itCore, members mutate.MemberStore) *mutate.Mutator {
	t.Helper()
	st, w := itStore(it, core), core.cfg.Work
	return built(mutate.New(mutate.Deps{
		Access: built(access.NewChecker(st, access.DefaultPolicy{}))(t), Messages: st, Edits: st, Hidden: st.Hidden(), Rooms: st,
		Events: droppedEvents{}, Interactions: st.Interactions(), Counts: st, Pins: st.Pins(),
		Projector: built(pinproj.New(st.Pins(), st))(t), Limits: core.cfg.Limits, Members: members, Requests: freshRequests{},
		Forget: noForget{}, Reads: st, Log: slog.New(slog.DiscardHandler),
		Timers:      built(work.NewTimers(it.js, w.Name, w.SubjectRoot, w.Partitions, core.cfg.MemberCountCheckDelay))(t),
		CountTimers: built(work.NewTimers(it.js, w.Name, w.SubjectRoot, w.Partitions, core.cfg.MessageCountCheckDelay))(t),
	}))(t)
}

func TestRealInfraACountMissedByACrashIsRepairedByItsTimer(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["CORE_REQUEST_DEADLINE"], env["MEMBER_COUNT_CHECK_DELAY"] = "2s", "2500ms"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	st := itStore(it, core)
	normal := createRoom(t, client)
	addMembersAs(t, client, itUser, normal, "timer-normal", "carol")
	if n := pendingTimers(t, it, core.cfg, parseRoom(t, normal)); n != 0 {
		t.Fatalf("a normal add left %d count check timers, want none", n)
	}

	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	repaired := itMetric(t, core.cfg, itMembersRepairedSeries)
	cmd := mutate.AddMembersCmd{Tenant: itTenant, User: itUser, Room: room, Users: []string{"carol", "dave"}, RequestID: "timer-crash"}
	added, err := mutatorWithoutEvents(t, it, core, countLost{st}).AddMembers(t.Context(), cmd)
	if err != nil || len(added) != 2 {
		t.Fatalf("AddMembers with a lost count = %v, %v; want both added", added, err)
	}
	if r, err := st.Get(t.Context(), room); err != nil || r.MemberCount != 2 || r.MemberCountVer != 1 || pendingTimers(t, it, core.cfg, room) != 1 {
		t.Fatalf("room after the lost count = %+v, %v; want 2 members at ver 1 and one armed timer", r, err)
	}
	fixed := awaitStored(t, "member count", func() (domain.Room, error) { return st.Get(t.Context(), room) },
		func(r domain.Room) bool { return r.MemberCount == 4 })
	ev := awaitLiveEvent(t, live, pbconv.MemberCountEventID(room, 2))
	if c := ev.GetMemberCountChanged(); fixed.MemberCountVer != 2 || c.GetMemberCount() != 4 || c.GetMemberCountVer() != 2 {
		t.Fatalf("repaired room ver %d, event %v; want 4 members at ver 2", fixed.MemberCountVer, ev)
	}
	if got := awaitMetric(t, core.cfg, itMembersRepairedSeries, func(v float64) bool { return v > repaired }); got != repaired+1 {
		t.Fatalf("%s = %v, want %v", itMembersRepairedSeries, got, repaired+1)
	}
}

func TestRealInfraLostMemberAndReadEventsAreRepublished(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	quiet := mutatorWithoutEvents(t, it, core, itStore(it, core))
	cmd := mutate.AddMembersCmd{Tenant: itTenant, User: itUser, Room: room, Users: []string{"erin"}, RequestID: "lost-erin"}
	if added, err := quiet.AddMembers(t.Context(), cmd); err != nil || len(added) != 1 {
		t.Fatalf("AddMembers without events = %v, %v; want erin added", added, err)
	}
	seq := sendAs(t, client, itUser, roomID, "lost-1", "erin reads this")
	pos, err := quiet.MarkRead(t.Context(), mutate.ReadCmd{Tenant: itTenant, User: "erin", Room: room, Seq: seq})
	if err != nil || pos.Seq != seq {
		t.Fatalf("MarkRead without events = %+v, %v; want read up to %d", pos, err, seq)
	}

	ids := []string{pbconv.MemberEventID(room, "erin", 1), pbconv.MemberCountEventID(room, 2), pbconv.ReadEventID(room, "erin", pos.Ver)}
	evs := awaitLiveEvents(t, live, ids...)
	if a := evs[ids[0]].GetMemberAdded(); a.GetUser() != "erin" || a.GetVer() != 1 || a.GetRole() != chatimv1.MemberRole_MEMBER_ROLE_MEMBER || a.GetReadVer() == 0 {
		t.Fatalf("republished member_added = %v, want erin as a member at ver 1 with a read position", a)
	}
	if c := evs[ids[1]].GetMemberCountChanged(); c.GetMemberCount() != 3 || c.GetMemberCountVer() != 2 {
		t.Fatalf("republished member_count_changed = %v, want 3 members at ver 2", c)
	}
	if r := evs[ids[2]].GetReadUpdated(); r.GetUser() != "erin" || r.GetReadSeq() != seq || r.GetReadVer() != pos.Ver {
		t.Fatalf("republished read_updated = %v, want erin at seq %d ver %d", r, seq, pos.Ver)
	}
}

func TestRealInfraReaderStateChangesEnterTheWorkStream(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	records := make(chan *nats.Msg, 256)
	sub, err := it.nc.ChanSubscribe(core.cfg.Work.SubjectRoot+".>", records)
	if err != nil {
		t.Fatalf("subscribe work subjects: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	first := sendAs(t, client, itUser, roomID, "work-1", "one")
	addMembersAs(t, client, itUser, roomID, "work-dave", "dave")
	second := sendAs(t, client, itUser, roomID, "work-2", "two")
	dave := callerAs(t.Context(), "dave")
	read, err := client.MarkRead(dave, &chatimv1.MarkReadRequest{RoomId: roomID, Seq: second})
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if _, err := client.ClearHistory(dave, &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, err := client.HideMessage(dave, &chatimv1.HideMessageRequest{RoomId: roomID, Seq: first}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}

	want := map[string]bool{"r:" + roomID: true, "m:" + pbconv.MessageEventID(room, 0, first): true, "m:" + pbconv.MessageEventID(room, 0, second): true}
	for _, user := range []string{itUser, "bob", "dave"} {
		want["g:"+pbconv.MemberEventID(room, user, 1)] = true
	}
	want["d:"+pbconv.ReadEventID(room, "dave", read.GetReadVer())] = true
	want["h:"+pbconv.HiddenEventID(room, "dave", 0, first)] = true
	cleared := "c:" + roomID + "-cl-dave-"
	assertRoomRecords(t, records, roomID, want, cleared)
	if doc := storedMembers(t, itStore(it, core), room, "dave")["dave"]; doc.Ver != 1 {
		t.Fatalf("dave = %+v, want ver 1 after read, clear and hide", doc)
	}
}
