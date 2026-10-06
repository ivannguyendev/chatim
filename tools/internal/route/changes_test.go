package route_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type roomLocator struct {
	mu    sync.Mutex
	rooms []uint64
}

func (l *roomLocator) Addr(room uint64) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rooms = append(l.rooms, room)
	return "core-1:9000", true
}

func (l *roomLocator) AnyAddr() (string, bool) { return "", false }

func (l *roomLocator) Refresh(context.Context) error { return nil }

func (l *roomLocator) asked() []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.rooms)
}

type changeCall func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error)

func reply[T proto.Message](resp T, st route.Stats, err error) (proto.Message, route.Stats, error) {
	return resp, st, err
}

var changeCalls = map[string]changeCall{
	"edit": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: 3, BaseVersion: 1, Text: "new"}))
	},
	"delete": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 3, BaseVersion: 2}))
	},
	"hide": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.HideMessage(ctx, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3}))
	},
	"clear": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 9}))
	},
	"edits": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 3, AfterVersion: 4, Limit: 10}))
	},
	"react": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 3, Emoji: "👍"}))
	},
	"pin": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, Seq: 3}))
	},
	"unpin": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 3}))
	},
}

var changeReplies = map[string]proto.Message{
	"edit":   &chatimv1.EditMessageResponse{Message: &chatimv1.Message{RoomId: "42", Seq: 3, Version: 2, Text: "new"}},
	"delete": &chatimv1.DeleteMessageResponse{Message: &chatimv1.Message{RoomId: "42", Seq: 3, Version: 3, Deleted: true}},
	"hide":   &chatimv1.HideMessageResponse{},
	"clear":  &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: 9},
	"edits":  &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Version: 5}}},
	"react":  &chatimv1.ReactMessageResponse{Change: 1, Reactions: &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}},
	"pin":    &chatimv1.PinMessageResponse{PinVersion: 1, Pins: []*chatimv1.Pin{{Seq: 3, PinVersion: 1}}},
	"unpin":  &chatimv1.UnpinMessageResponse{PinVersion: 2},
}

func TestChangeCallsRouteByRoomAndRetryAttemptTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core := &fakeCore{}
		loc := &roomLocator{}
		c := newClient(t, loc, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{Attempt: time.Second})
		for name, call := range changeCalls {
			core.block = 1
			got, st, err := call(t.Context(), c, "42")
			if err != nil || st.Attempts != 2 || !proto.Equal(got, changeReplies[name]) {
				t.Fatalf("%s = %v, %v after %d attempts; want %v on the second attempt", name, got, err, st.Attempts, changeReplies[name])
			}
		}
		rooms := loc.asked()
		if len(rooms) != 2*len(changeCalls) || slices.ContainsFunc(rooms, func(r uint64) bool { return r != 42 }) {
			t.Fatalf("locator asked for rooms %v, want room 42 on every attempt", rooms)
		}
	})
}

func TestChangeCallsKeepConflictsAndBadRoomIDsLocal(t *testing.T) {
	core := &fakeCore{fail: []error{status.Error(codes.FailedPrecondition, "message version conflict")}}
	c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{})
	if _, st, err := changeCalls["edit"](t.Context(), c, "42"); status.Code(err) != codes.FailedPrecondition || st.Attempts != 1 {
		t.Fatalf("edit on a stale base = %v after %d attempts, want FailedPrecondition at once", err, st.Attempts)
	}
	for name, call := range changeCalls {
		if _, st, err := call(t.Context(), c, "not-a-room"); status.Code(err) != codes.InvalidArgument || st.Attempts != 0 {
			t.Fatalf("%s with a bad room id = %v with %+v, want a local InvalidArgument", name, err, st)
		}
	}
}
