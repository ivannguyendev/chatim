package pbconv_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func pinFact(op domain.PinOp, pv uint64) domain.PinAction {
	return domain.PinAction{Room: 9_007_199_254_740_993, PV: pv, Tenant: "acme", Op: op, Seq: 7, By: "bob", At: reactedAt}
}

func pinEnvelope(id string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(reactedAt),
	}
}

func TestPinChangedPicksTheEventByOp(t *testing.T) {
	m := sample()
	wantPin := pinEnvelope("9007199254740993-p3")
	wantPin.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{Message: pbconv.Message(m), PinVer: 3}}
	wantUnpin := pinEnvelope("9007199254740993-p4")
	wantUnpin.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{Message: pbconv.Message(m), PinVer: 4}}
	pin, unpin := pinFact(domain.PinOpPin, 3), pinFact(domain.PinOpUnpin, 4)
	cases := []struct {
		name      string
		got, want *chatimv1.Event
	}{
		{"pinned", pbconv.MessagePinned(domain.RoomGroup, m, pin), wantPin},
		{"unpinned", pbconv.MessageUnpinned(domain.RoomGroup, m, unpin), wantUnpin},
		{"changed by a pin fact", pbconv.PinChanged(domain.RoomGroup, m, pin), wantPin},
		{"changed by an unpin fact", pbconv.PinChanged(domain.RoomGroup, m, unpin), wantUnpin},
	}
	for _, c := range cases {
		if !proto.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestPinChangedDropsTheTextOfADeletedMessage(t *testing.T) {
	m := sample()
	m.Deleted, m.Version = true, 2
	got := pbconv.PinChanged(domain.RoomGroup, m, pinFact(domain.PinOpUnpin, 5)).GetMessageUnpinned().GetMessage()
	if got.GetText() != "" || !got.GetDeleted() || got.GetSeq() != 7 {
		t.Fatalf("snapshot of a deleted message = %v, want seq 7, deleted and no text", got)
	}
}

func TestPinsListsEveryPin(t *testing.T) {
	pins := []domain.Pin{{Seq: 9, By: "bob", At: reactedAt, PV: 4}, {Thread: 3, Seq: 7, By: "alice", At: sentAt, PV: 1}}
	want := []*chatimv1.Pin{
		{Seq: 9, By: "bob", PinnedAt: timestamppb.New(reactedAt), PinVer: 4},
		{ThreadRoot: 3, Seq: 7, By: "alice", PinnedAt: timestamppb.New(sentAt), PinVer: 1},
	}
	if got := pbconv.Pins(pins); !slices.EqualFunc(got, want, func(a, b *chatimv1.Pin) bool { return proto.Equal(a, b) }) {
		t.Fatalf("Pins = %v, want %v", got, want)
	}
	if got := pbconv.Pins(nil); len(got) != 0 {
		t.Fatalf("Pins(nil) = %v, want empty", got)
	}
}
