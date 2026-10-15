package eventkit_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type countingRooms struct {
	mu    sync.Mutex
	rooms map[uint64]domain.Room
	reads int
}

func (c *countingRooms) Get(_ context.Context, id uint64) (domain.Room, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	r, ok := c.rooms[id]
	if !ok {
		return domain.Room{}, domain.ErrRoomNotFound
	}
	return r, nil
}

func (c *countingRooms) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func newCountingRooms() *countingRooms {
	return &countingRooms{rooms: map[uint64]domain.Room{
		1: {ID: 1, Tenant: "t1", Type: domain.RoomGroup, Name: "one"},
		2: {ID: 2, Tenant: "t2", Type: domain.RoomDM, Name: "two"},
	}}
}

func TestRoomTypesReadsEachRoomOnceAndKeepsOnlyIdentity(t *testing.T) {
	rooms := newCountingRooms()
	c := eventkit.NewRoomTypes(rooms, 10)
	for range 3 {
		got, err := c.Identity(t.Context(), 1)
		if err != nil || got != (domain.Room{ID: 1, Tenant: "t1", Type: domain.RoomGroup}) {
			t.Fatalf("Identity = %+v, %v", got, err)
		}
	}
	if typ, err := c.Type(t.Context(), 2); err != nil || typ != domain.RoomDM {
		t.Fatalf("Type = %v, %v", typ, err)
	}
	if rooms.count() != 2 {
		t.Fatalf("store reads = %d, want 2", rooms.count())
	}
}

func TestRoomTypesDoesNotCacheMisses(t *testing.T) {
	rooms := newCountingRooms()
	c := eventkit.NewRoomTypes(rooms, 10)
	for range 2 {
		if _, err := c.Type(t.Context(), 9); !errors.Is(err, domain.ErrRoomNotFound) {
			t.Fatalf("Type of a missing room = %v", err)
		}
	}
	if rooms.count() != 2 {
		t.Fatalf("store reads = %d, want 2", rooms.count())
	}
}

func TestRoomTypesClearsTheCacheAtTheLimit(t *testing.T) {
	rooms := newCountingRooms()
	c := eventkit.NewRoomTypes(rooms, 1)
	for _, id := range []uint64{1, 2, 1} {
		if _, err := c.Identity(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	if rooms.count() != 3 {
		t.Fatalf("store reads = %d, want 3", rooms.count())
	}
}
