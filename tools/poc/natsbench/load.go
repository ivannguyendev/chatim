package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

func load(ctx context.Context, c config, js jetstream.JetStream) error {
	conns := make([]*nats.Conn, c.conns)
	inboxes := make([]chan *nats.Msg, c.conns)
	for i := range conns {
		nc, err := nats.Connect(c.url, nats.Name(fmt.Sprintf("gateway-%d", i)))
		if err != nil {
			return fmt.Errorf("gateway connect: %w", err)
		}
		defer nc.Close()
		conns[i], inboxes[i] = nc, make(chan *nats.Msg, 65_536)
	}

	start := time.Now()
	subs := make([]*nats.Subscription, 0, c.subs)
	for room := 0; room < c.subs; room++ {
		i := room % c.conns
		sub, err := conns[i].ChanSubscribe(fmt.Sprintf("live.t1.room.%d.>", room), inboxes[i])
		if err != nil {
			return fmt.Errorf("subscribe room %d: %w", room, err)
		}
		subs = append(subs, sub)
	}
	for _, nc := range conns {
		if err := nc.Flush(); err != nil {
			return err
		}
	}
	fmt.Printf("subscribed %d rooms on %d connections in %v; %s\n", c.subs, c.conns, time.Since(start).Round(time.Millisecond), serverMem(c.monitor))

	var e2e, ack latency.Recorder
	var received, published, failed atomic.Int64
	recvCtx, stopRecv := context.WithCancel(ctx)
	var recvWG sync.WaitGroup
	for _, inbox := range inboxes {
		recvWG.Go(func() {
			for {
				select {
				case m := <-inbox:
					sent := time.Unix(0, int64(binary.BigEndian.Uint64(m.Data)))
					e2e.Add(time.Since(sent))
					received.Add(1)
				case <-recvCtx.Done():
					return
				}
			}
		})
	}

	pubCtx, cancel := context.WithTimeout(ctx, c.duration)
	defer cancel()
	var pubWG sync.WaitGroup
	interval := c.interval()
	for p := 0; p < c.publishers; p++ {
		pubWG.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-pubCtx.Done():
					return
				case <-tick.C:
				}
				subj := fmt.Sprintf("evt.t1.room.%d.msg_created", rng.IntN(c.subs))
				begin := time.Now()
				if _, err := js.Publish(ctx, subj, stamp(c.size)); err != nil {
					failed.Add(1)
					continue
				}
				ack.Add(time.Since(begin))
				published.Add(1)
			}
		})
	}
	pubWG.Wait()
	awaitDelivered(ctx, &received, &published)
	stopRecv()
	recvWG.Wait()

	dropped, err := totalDropped(subs)
	if err != nil {
		return err
	}
	fmt.Printf("target=%d/s published=%d (%.0f/s) failed=%d received=%d dropped=%d; %s\n",
		c.rate, published.Load(), float64(published.Load())/c.duration.Seconds(), failed.Load(), received.Load(), dropped, serverMem(c.monitor))
	fmt.Printf("jetstream publish ack: %v\n", ack.Summary())
	fmt.Printf("publish -> gateway:    %v\n", e2e.Summary())
	return nil
}

func awaitDelivered(ctx context.Context, received, published *atomic.Int64) {
	deadline := time.Now().Add(5 * time.Second)
	for received.Load() < published.Load() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

func totalDropped(subs []*nats.Subscription) (int, error) {
	total := 0
	for _, sub := range subs {
		n, err := sub.Dropped()
		if err != nil {
			return 0, fmt.Errorf("dropped count: %w", err)
		}
		total += n
	}
	return total, nil
}
