package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/pkg/coreroute"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func parallel(ctx context.Context, n, workers int, do func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var next atomic.Int64
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for range min(workers, n) {
		wg.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= n || ctx.Err() != nil {
					return
				}
				if err := do(ctx, i); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					cancel()
					return
				}
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("%d failed: %w", len(errs), errs[0])
	}
	return ctx.Err()
}

func seedRooms(ctx context.Context, cl *route.Client, c config, run string, specs []roomSpec) ([]string, error) {
	out := make([]string, len(specs))
	err := parallel(ctx, len(specs), c.workers, func(ctx context.Context, i int) error {
		s := specs[i]
		req := &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: fmt.Sprintf("rlb %s #%d", run, i), Members: s.members, RequestId: fmt.Sprintf("%s-r%d", run, i)}
		resp, _, err := cl.CreateRoom(coreroute.WithCaller(ctx, c.tenant, s.creator), req)
		if err != nil {
			return fmt.Errorf("create room %d: %w", i, err)
		}
		out[i] = resp.GetRoom().GetId()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create rooms: %w", err)
	}
	return out, nil
}

func sendMessages(ctx context.Context, cl *route.Client, c config, run, group string, specs []roomSpec, ids []string) (int, error) {
	var sent atomic.Int64
	err := parallel(ctx, len(specs), c.workers, func(ctx context.Context, i int) error {
		s := specs[i]
		for k := range s.messages {
			req := &chatimv1.SendMessageRequest{RoomId: ids[i], Cid: fmt.Sprintf("%s-%s%d-m%d", run, group, i, k), Text: fmt.Sprintf("tin %d của phòng %s%d", k+1, group, i)}
			if _, _, err := cl.SendMessage(coreroute.WithCaller(ctx, c.tenant, s.creator), req); err != nil {
				return fmt.Errorf("send to room %d: %w", i, err)
			}
			sent.Add(1)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("send %s messages: %w", group, err)
	}
	return int(sent.Load()), nil
}

func assignRooms(l layout, specs []roomSpec, ids []string) error {
	if len(specs) != len(ids) {
		return errors.New("room ids do not match the layout")
	}
	for i, s := range specs {
		if s.private != nil {
			s.private.rooms = append(s.private.rooms, ids[i])
		}
	}
	for _, u := range l.users {
		if len(u.rooms) == 0 {
			return fmt.Errorf("user %s has no private room", u.name)
		}
	}
	return nil
}
