package projection

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const RoomActivityName = "room_activity"

var roomActivityKinds = [...]struct{ data, kind string }{
	{"message", "msg_created"},
	{"message", "msg_edited"},
	{"message", "msg_deleted"},
	{"message", "reaction_changed"},
	{"room", "msg_pinned"},
	{"room", "msg_unpinned"},
	{"member", "member_added"},
	{"member", "member_removed"},
	{"member", "member_role_changed"},
	{"member", "member_priority_changed"},
	{"member", "bookmark_changed"},

	{"room", "room_updated"},
	{"member", "room_preferences_changed"},
}

type ActivityWriter interface {
	TouchActivity(ctx context.Context, acts []store.Activity) error
}

type RoomActivity struct {
	rooms ActivityWriter
}

type activityKey struct {
	room, thread uint64
	msg, edited  bool
}

func NewRoomActivity(rooms ActivityWriter) *RoomActivity { return &RoomActivity{rooms: rooms} }

func (a *RoomActivity) Name() string { return RoomActivityName }

func (a *RoomActivity) Subjects(root string) []string {
	out := make([]string, len(roomActivityKinds))
	for i, k := range roomActivityKinds {
		out[i] = filter(root, k.data, k.kind)
	}
	return out
}

func (a *RoomActivity) Handle(ctx context.Context, evs []Event) []error {
	errs := make([]error, len(evs))
	at := make(map[activityKey]int, len(evs))
	acts := make([]store.Activity, 0, len(evs))
	touched := make([]int, 0, len(evs))
	for i, ev := range evs {
		act, msg, ok := activityOf(ev)
		if !ok {
			errs[i] = fmt.Errorf("%w: room activity does not keep %T", apperr.ErrInvalidArgument, ev.Proto.GetPayload())
			continue
		}
		touched = append(touched, i)
		acts = merged(acts, at, activityKey{act.Room, act.Thread, msg, act.Edited}, act)
	}
	if len(acts) == 0 {
		return errs
	}
	if err := a.rooms.TouchActivity(ctx, acts); err != nil {
		err = fmt.Errorf("room activity: %w", err)
		for _, i := range touched {
			errs[i] = err
		}
	}
	return errs
}

func merged(acts []store.Activity, at map[activityKey]int, k activityKey, act store.Activity) []store.Activity {
	i, ok := at[k]
	if !ok {
		at[k] = len(acts)
		return append(acts, act)
	}
	acts[i].Seq = max(acts[i].Seq, act.Seq)
	if act.At.After(acts[i].At) {
		acts[i].At = act.At
	}
	return acts
}

func activityOf(ev Event) (act store.Activity, msg, ok bool) {
	act = store.Activity{Room: ev.Room, Thread: ev.Proto.GetThreadRoot(), At: ev.At}
	switch ev.Proto.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		act.Seq = ev.Proto.GetSeq()
		return act, true, true
	case *chatimv1.Event_MessageEdited, *chatimv1.Event_MessageDeleted:
		act.Edited = act.Thread == 0
		return act, false, true
	case *chatimv1.Event_ReactionChanged, *chatimv1.Event_MessagePinned, *chatimv1.Event_MessageUnpinned,
		*chatimv1.Event_MemberAdded, *chatimv1.Event_MemberRemoved, *chatimv1.Event_MemberRoleChanged,
		*chatimv1.Event_MemberPriorityChanged, *chatimv1.Event_BookmarkChanged, *chatimv1.Event_RoomUpdated,
		*chatimv1.Event_RoomPreferencesChanged:
		return act, false, true
	default:
		return store.Activity{}, false, false
	}
}
