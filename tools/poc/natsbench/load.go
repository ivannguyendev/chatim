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
	for room := 0; room < c.subs; room++ {
		i := room % c.conns

		if _, err := conns[i].ChanSubscribe(fmt.Sprintf("live.t1.room.%d.>", room), inboxes[i]); err != nil {
			return fmt.Errorf("subscribe room %d: %w", room, err)
		}
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
	interval := time.Duration(float64(time.Second) * float64(c.publishers) / float64(c.rate))
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
	time.Sleep(time.Second)
	stopRecv()
	recvWG.Wait()

	fmt.Printf("published=%d (%.0f/s) failed=%d received=%d; %s\n",
		published.Load(), float64(published.Load())/c.duration.Seconds(), failed.Load(), received.Load(), serverMem(c.monitor))
	fmt.Printf("jetstream publish ack: %v\n", ack.Summary())
	fmt.Printf("publish -> gateway:    %v\n", e2e.Summary())
	return nil
}
