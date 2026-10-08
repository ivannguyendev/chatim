package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const recountRoom uint64 = 7_350_000_001

var recountAt = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

type sentEvents struct{ msgs []*nats.Msg }

func (s *sentEvents) PublishMsg(_ context.Context, m *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	s.msgs = append(s.msgs, m)
	return &jetstream.PubAck{}, nil
}

type racingCounts struct{ *memstore.Rooms }

func (r racingCounts) CountMembers(ctx context.Context, room uint64) (int, error) {
	if _, err := r.AddMemberCount(ctx, room, 1); err != nil {
		return 0, err
	}
	return r.Rooms.CountMembers(ctx, room)
}

func driftedRooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rooms := memstore.NewRooms()
	r := domain.Room{ID: recountRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: recountAt, MemberCount: 2}
	members := []domain.Member{
		{Room: recountRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: recountAt},
		{Room: recountRoom, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: recountAt},
	}
	if err := rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok, err := rooms.SetMemberCount(t.Context(), recountRoom, 1, 5); err != nil || !ok {
		t.Fatalf("SetMemberCount(drift) = %v, %v", ok, err)
	}
	return rooms
}

func newRecounter(rooms recountRooms, pub *sentEvents, out *bytes.Buffer) recounter {
	return recounter{rooms: rooms, pub: pub, root: "evt", now: func() time.Time { return recountAt }, out: out}
}

func TestRecountDryRunPrintsBothCountsAndWritesNothing(t *testing.T) {
	rooms, pub, out := driftedRooms(t), &sentEvents{}, &bytes.Buffer{}
	if err := newRecounter(rooms, pub, out).run(t.Context(), RecountOptions{Room: recountRoom, DryRun: true}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); got != "room=7350000001 stored=5 counted=2\n" {
		t.Fatalf("output = %q", got)
	}
	r, err := rooms.Get(t.Context(), recountRoom)
	if err != nil || r.MemberCount != 5 || r.MemberCountVer != 2 || len(pub.msgs) != 0 {
		t.Fatalf("after dry run room = %+v, %v with %d events; want the drift kept and nothing sent", r, err, len(pub.msgs))
	}
}

func TestRecountRestoresTheCountAndPublishesIt(t *testing.T) {
	rooms, pub, out := driftedRooms(t), &sentEvents{}, &bytes.Buffer{}
	if err := newRecounter(rooms, pub, out).run(t.Context(), RecountOptions{Room: recountRoom}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); got != "room=7350000001 stored=5 counted=2\nmember_count_ver=3\n" {
		t.Fatalf("output = %q", got)
	}
	r, err := rooms.Get(t.Context(), recountRoom)
	if err != nil || r.MemberCount != 2 || r.MemberCountVer != 3 {
		t.Fatalf("room = %+v, %v; want 2 members at ver 3", r, err)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].Header.Get(jetstream.MsgIDHeader) != pbconv.MemberCountEventID(recountRoom, 3) ||
		!strings.HasSuffix(pub.msgs[0].Subject, ".member_count_changed") {
		t.Fatalf("events = %+v, want one member_count_changed at ver 3", pub.msgs)
	}
}

func TestRecountFailsWhenTheCountMovesMeanwhile(t *testing.T) {
	rooms, pub, out := driftedRooms(t), &sentEvents{}, &bytes.Buffer{}
	err := newRecounter(racingCounts{rooms}, pub, out).run(t.Context(), RecountOptions{Room: recountRoom})
	if !errors.Is(err, errRecountRaced) || len(pub.msgs) != 0 {
		t.Fatalf("run = %v with %d events, want errRecountRaced and nothing sent", err, len(pub.msgs))
	}
}

func TestRecountOfAMissingRoomFails(t *testing.T) {
	err := newRecounter(memstore.NewRooms(), &sentEvents{}, &bytes.Buffer{}).run(t.Context(), RecountOptions{Room: recountRoom})
	if !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("run = %v, want ErrRoomNotFound", err)
	}
}

func TestParseRecountArgs(t *testing.T) {
	got, err := parseRecountArgs([]string{"-room", "7350000001", "-dry-run"}, &bytes.Buffer{})
	if err != nil || got != (RecountOptions{Room: recountRoom, DryRun: true}) {
		t.Fatalf("parse = %+v, %v", got, err)
	}
	for _, args := range [][]string{nil, {"-dry-run"}, {"-room", "0"}, {"-room", "abc"}, {"-room", "1", "extra"}} {
		if _, err := parseRecountArgs(args, &bytes.Buffer{}); err == nil {
			t.Errorf("parse(%v) = nil, want an error", args)
		}
	}
}
