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
	if ev.GetTenant() == "" || ev.GetSeq() == 0 || ev.GetTs().CheckValid() != nil {
		return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: tenant, seq and ts are required", errInvalidEvent)
	}
	out := attach.Event{Tenant: ev.GetTenant(), Ref: domain.MessageRef{Room: ids.FormatRoomID(room), Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()}}
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		out.HasFiles = len(p.MessageCreated.GetMessage().GetAttachments()) > 0
		return attach.Created, out, ev.GetTs().AsTime(), nil
	case *chatimv1.Event_MessageDeleted:
		return attach.Deleted, out, ev.GetTs().AsTime(), nil
	}
	return 0, attach.Event{}, time.Time{}, fmt.Errorf("%w: payload is not a created or deleted message", errInvalidEvent)
}
