package consumer

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errInvalidEvent = errors.New("invalid media event")

type job struct {
	d    *tracked
	kind attach.Kind
	ev   attach.Event
}

func decode(data []byte) (attach.Kind, attach.Event, time.Time, error) {
	ev := &chatimv1.Event{}
	if err := proto.Unmarshal(data, ev); err != nil {
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: %w", errInvalidEvent, err)
	}
	room, err := ids.ParseRoomID(ev.GetRoomId())
	if err != nil {
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: %w", errInvalidEvent, err)
	}
	if ev.GetTenant() == "" || ev.GetTs().CheckValid() != nil {
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: tenant and ts are required", errInvalidEvent)
	}
	out := attach.Event{Tenant: ev.GetTenant(), Ref: domain.MessageRef{Room: ids.FormatRoomID(room)}}
	var kind attach.Kind
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_RoomUpdated:
		return attach.RoomUpdated, out, ev.GetTs().AsTime(), nil
	case *chatimv1.Event_MessageCreated:
		kind, out.HasFiles = attach.Created, len(p.MessageCreated.GetMessage().GetAttachments()) > 0
	case *chatimv1.Event_MessageDeleted:
		kind = attach.Deleted
	default:
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: payload is not a message or room update", errInvalidEvent)
	}
	if ev.GetSeq() == 0 {
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: a message event needs a seq", errInvalidEvent)
	}
	out.Ref.Thread, out.Ref.Seq = ev.GetThreadRoot(), ev.GetSeq()
	return kind, out, ev.GetTs().AsTime(), nil
}
