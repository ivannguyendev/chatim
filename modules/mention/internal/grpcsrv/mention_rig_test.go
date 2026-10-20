package grpcsrv_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/groups"
	"github.com/ivannguyendev/chatim/modules/mention/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/modules/mention/internal/roomgroups"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	toBob     = domain.Target{Kind: domain.TargetUser, ID: "bob"}
	toOps     = domain.Target{Kind: domain.TargetGroup, ID: "ops"}
	toHanoi   = domain.Target{Kind: domain.TargetGroup, ID: "hanoi"}
	toAll     = domain.Target{Kind: domain.TargetAll}
	mentionT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

const rigSyncMax = 3

type options struct {
	hide    []string
	listing func(store.Mentions) store.Mentions
}

type mentionRig struct {
	svc        *grpcsrv.Service
	deps       grpcsrv.Deps
	core       *fakeCore
	mentions   *memstore.Mentions
	rooms      *memstore.Rooms
	userGroups *memstore.UserGroups
	groups     *memstore.Groups
	sets       *roomgroups.Sets
	alice, bob context.Context
	seqs       map[[2]uint64]uint64
	nextRoom   uint64
}

func as(t *testing.T, tenant, user string) context.Context {
	return metadata.NewIncomingContext(t.Context(), metadata.Pairs("x-chatim-tenant", tenant, "x-chatim-user", user))
}

func newMentionRig(t *testing.T, o options) *mentionRig {
	t.Helper()
	rg := &mentionRig{
		core: newFakeCore(), mentions: memstore.NewMentions(), rooms: memstore.NewRooms(), userGroups: memstore.NewUserGroups(),
		groups: memstore.NewGroups(), alice: as(t, "acme", "alice"), bob: as(t, "acme", "bob"), seqs: map[[2]uint64]uint64{}, nextRoom: 1000,
	}
	builder, err := roomgroups.NewBuilder(rg.core, rg.userGroups)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	cfg := roomgroups.Config{SyncMax: rigSyncMax, TTL: time.Minute, Rebuild: 10 * time.Minute}
	if rg.sets, err = roomgroups.NewSets(cfg, builder.Build, quiet); err != nil {
		t.Fatalf("NewSets: %v", err)
	}
	t.Cleanup(rg.sets.Close)
	var listing store.Mentions = rg.mentions
	if o.listing != nil {
		listing = o.listing(rg.mentions)
	}
	rg.deps = grpcsrv.Deps{
		Core: rg.core, Mentions: listing, Rooms: rg.rooms, UserGroups: rg.userGroups, Groups: rg.groups,
		Catalog: groups.New(rg.groups), RoomGroups: rg.sets, HideRead: o.hide,
	}
	rg.svc = rg.service(t, func(*grpcsrv.Deps) {})
	rg.catalog(t, domain.Group{ID: "ops", Name: "Ops", Active: true, Ver: 1}, domain.Group{ID: "hanoi", Name: "Hanoi", Active: true, Ver: 1})
	return rg
}

func (rg *mentionRig) service(t *testing.T, change func(*grpcsrv.Deps)) *grpcsrv.Service {
	t.Helper()
	d := rg.deps
	change(&d)
	svc, err := grpcsrv.New(d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func sec(n int) time.Time { return mentionT0.Add(time.Duration(n) * time.Second) }

func (rg *mentionRig) catalog(t *testing.T, items ...domain.Group) {
	t.Helper()
	for i := range items {
		items[i].Tenant = "acme"
	}
	if _, err := rg.groups.Set(t.Context(), items); err != nil {
		t.Fatalf("Groups.Set: %v", err)
	}
}

func (rg *mentionRig) room(t *testing.T, at int, members ...string) uint64 {
	t.Helper()
	rg.nextRoom++
	for _, u := range append([]string{"alice"}, members...) {
		rg.join(t, rg.nextRoom, at, u)
	}
	return rg.nextRoom
}

func (rg *mentionRig) join(t *testing.T, room uint64, at int, user string) {
	t.Helper()
	rg.core.setMember(room, user, func(m *domain.MemberRoom) { m.JoinedAt = sec(at) })
}

func (rg *mentionRig) clear(t *testing.T, room uint64, at int) {
	t.Helper()
	rg.core.setMember(room, "bob", func(m *domain.MemberRoom) { m.ClearedAt = sec(at) })
}

func (rg *mentionRig) read(t *testing.T, room, seq uint64) {
	t.Helper()
	rg.core.setMember(room, "bob", func(m *domain.MemberRoom) { m.ReadSeq = seq })
}

func (rg *mentionRig) post(t *testing.T, room uint64, text string, at int, targets ...domain.Target) uint64 {
	t.Helper()
	return rg.postIn(t, room, 0, text, at, targets...)
}

func (rg *mentionRig) postIn(t *testing.T, room, thread uint64, text string, at int, targets ...domain.Target) uint64 {
	t.Helper()
	rg.seqs[[2]uint64{room, thread}]++
	key := domain.MessageKey{Room: room, Thread: thread, Seq: rg.seqs[[2]uint64{room, thread}]}
	msg := &chatimv1.Message{Sender: "alice", Text: text, Cid: fmt.Sprintf("c-%d-%d-%d", room, thread, key.Seq)}
	plan := domain.Plan{Key: key, Tenant: "acme", Sender: "alice"}
	for _, tg := range targets {
		if tg.At.IsZero() {
			tg.At = sec(at)
		}
		plan.Live = append(plan.Live, tg)
		switch tg.Kind {
		case domain.TargetAll:
			msg.MentionAll, msg.MentionAllAt = true, timestamppb.New(tg.At)
		case domain.TargetUser:
			msg.MentionTargets = append(msg.MentionTargets, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: tg.ID, At: timestamppb.New(tg.At)})
		case domain.TargetGroup:
			msg.MentionTargets = append(msg.MentionTargets, &chatimv1.MentionTarget{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: tg.ID, At: timestamppb.New(tg.At)})
		}
		if tg.Kind != domain.TargetUser {
			if err := rg.rooms.Raise(t.Context(), "acme", room, tg.At); err != nil {
				t.Fatalf("Raise: %v", err)
			}
		}
	}
	rg.core.putMessage(key, sec(at), msg)
	if len(plan.Live) > 0 {
		if err := rg.mentions.Apply(t.Context(), plan); err != nil {
			t.Fatalf("Apply(%s): %v", text, err)
		}
	}
	return key.Seq
}

func (rg *mentionRig) list(t *testing.T, req *chatimv1.ListMentionsRequest) (texts []string, read []bool, next string) {
	t.Helper()
	resp, err := rg.svc.ListMentions(rg.bob, req)
	if err != nil {
		t.Fatalf("ListMentions(%v): %v", req, err)
	}
	for _, it := range resp.GetItems() {
		texts = append(texts, it.GetMessage().GetText())
		read = append(read, it.GetRead())
	}
	return texts, read, resp.GetNext()
}
