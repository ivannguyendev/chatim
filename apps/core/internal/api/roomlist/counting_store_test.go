package roomlist_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

var errBroken = errors.New("broken tail read")

type windowCall struct{ room, after uint64 }

type countingStore struct {
	rooms      *memstore.Rooms
	msgs       *memstore.Messages
	hidden     *memstore.Hidden
	mu         sync.Mutex
	calls      map[string]int
	windows    []windowCall
	brokenRoom uint64
}

func (c *countingStore) count(op string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[op]++
}

func (c *countingStore) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls, c.windows = nil, nil
}

func (c *countingStore) snapshot() (map[string]int, []windowCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.calls), append([]windowCall(nil), c.windows...)
}

func (c *countingStore) expectCalls(t *testing.T, want map[string]int) {
	t.Helper()
	got, _ := c.snapshot()
	if got == nil {
		got = map[string]int{}
	}
	if !maps.Equal(got, want) {
		t.Errorf("store calls = %v, want %v", got, want)
	}
}

func (c *countingStore) CountRooms(ctx context.Context, tenant, user string, limit int) (int, error) {
	c.count("CountRooms")
	return c.rooms.CountRooms(ctx, tenant, user, limit)
}

func (c *countingStore) JoinedRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	c.count("JoinedRoomOrder")
	return c.rooms.JoinedRoomOrder(ctx, q)
}

func (c *countingStore) StepwiseRoomOrder(ctx context.Context, q store.RoomListQuery) ([]store.RoomOrderRow, error) {
	c.count("StepwiseRoomOrder")
	return c.rooms.StepwiseRoomOrder(ctx, q)
}

func (c *countingStore) MembersByKeys(ctx context.Context, keys []store.MemberKey) ([]domain.Member, error) {
	c.count("MembersByKeys")
	return c.rooms.MembersByKeys(ctx, keys)
}

func (c *countingStore) RoomActivities(ctx context.Context, rooms []uint64) ([]store.RoomActivity, error) {
	c.count("RoomActivities")
	return c.rooms.RoomActivities(ctx, rooms)
}

func (c *countingStore) RoomInfos(ctx context.Context, rooms []uint64) ([]domain.Room, error) {
	c.count("RoomInfos")
	return c.rooms.RoomInfos(ctx, rooms)
}

func (c *countingStore) TailWindow(ctx context.Context, room, afterSeq uint64, limit int) ([]domain.TailMessage, error) {
	c.count("TailWindow")
	c.mu.Lock()
	c.windows = append(c.windows, windowCall{room: room, after: afterSeq})
	broken := c.brokenRoom == room
	c.mu.Unlock()
	if broken {
		return nil, errBroken
	}
	return c.msgs.TailWindow(ctx, room, afterSeq, limit)
}

func (c *countingStore) TailPreviews(ctx context.Context, keys []store.MsgKey) ([]domain.Message, error) {
	c.count("TailPreviews")
	return c.msgs.TailPreviews(ctx, keys)
}

func (c *countingStore) HiddenInRooms(ctx context.Context, user string, ranges []store.HiddenRange) (map[uint64][]uint64, error) {
	c.count("HiddenInRooms")
	return c.hidden.HiddenInRooms(ctx, user, ranges)
}
