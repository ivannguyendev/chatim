package projection

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var ErrBadEvent = errors.New("malformed event")

type Event struct {
	Room  uint64
	At    time.Time
	Proto *chatimv1.Event
}

func Decode(data []byte) (Event, error) {
	ev := &chatimv1.Event{}
	if err := proto.Unmarshal(data, ev); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrBadEvent, err)
	}
	room, err := strconv.ParseUint(ev.GetRoomId(), 10, 64)
	switch {
	case err != nil || room == 0:
		return Event{}, fmt.Errorf("%w: room id %q", ErrBadEvent, ev.GetRoomId())
	case ev.GetTs().CheckValid() != nil || ev.GetTs().GetSeconds() <= 0:
		return Event{}, fmt.Errorf("%w: event %s has no valid time", ErrBadEvent, ev.GetId())
	case ev.GetPayload() == nil:
		return Event{}, fmt.Errorf("%w: event %s has no payload", ErrBadEvent, ev.GetId())
	}
	return Event{Room: room, At: ev.GetTs().AsTime(), Proto: ev}, nil
}

func filter(root, data, kind string) string { return root + ".*." + data + ".*." + kind }
