package publish_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func roomCreated(room uint64) *chatimv1.Event {
	return pbconv.RoomCreated(domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 1})
}

func TestRoomCreatedGoesToItsOwnSubject(t *testing.T) {
	m, err := publish.Message("evt", roomA, roomCreated(roomA))
	if err != nil {
		t.Fatalf("Message(room_created): %v", err)
	}
	if m.Subject != "evt.acme.room.101.room_created" || publishtest.MsgID(m) != "101-created" {
		t.Fatalf("subject %q msg id %q, want evt.acme.room.101.room_created and 101-created", m.Subject, publishtest.MsgID(m))
	}
}

func TestRoomCreatedIsPublishedButNeverMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		if err := rg.Enqueue(roomA, []*chatimv1.Event{roomCreated(roomA)}); err != nil {
			t.Fatalf("Enqueue(room_created): %v", err)
		}
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"101-created", "101-0-1"}) {
			t.Fatalf("stored %v, want 101-created then 101-0-1", got)
		}
		if got := m.marked(); !slices.Equal(got, []store.MsgKey{{Room: roomA, Seq: 1}}) {
			t.Fatalf("marked %v, want only the message", got)
		}
		if key, ok := publish.MarkKey(roomA, roomCreated(roomA)); ok {
			t.Fatalf("MarkKey(room_created) = %v, true; want no mark", key)
		}
	})
}
