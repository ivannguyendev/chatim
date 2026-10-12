package main

import (
	"context"
	"fmt"
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func createRooms(ctx context.Context, cl *route.Client, c config, run string) ([]room, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rooms := make([]room, c.rooms)
	next := make(chan int)
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for range min(c.workers, c.rooms) {
		wg.Go(func() {
			for i := range next {
				r, err := createRoom(ctx, cl, c, run, i)
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					cancel()
					continue
				}
				rooms[i] = r
			}
		})
	}
feed:
	for i := range c.rooms {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if len(errs) > 0 {
		return nil, fmt.Errorf("create rooms (%d failed): %w", len(errs), errs[0])
	}
	return rooms, ctx.Err()
}

func createRoom(ctx context.Context, cl *route.Client, c config, run string, i int) (room, error) {
	members := make([]string, c.members)
	for k := range members {
		members[k] = fmt.Sprintf("%s-u%d-%d", run, i, k)
	}
	req := &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: fmt.Sprintf("bench %s #%d", run, i), Members: members, RequestId: fmt.Sprintf("%s-r%d", run, i)}
	resp, _, err := cl.CreateRoom(route.WithCaller(ctx, c.tenant, members[0]), req)
	if err != nil {
		return room{}, fmt.Errorf("room %d: %w", i, err)
	}
	return room{id: resp.GetRoom().GetId(), members: members}, nil
}
