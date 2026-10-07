package main

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
)

const (
	flushTimeout = 5 * time.Second
	awaitPoll    = 50 * time.Millisecond
)

type liveWatch struct {
	nc        *nats.Conn
	live      *openloop.Live
	rooms     int
	undecoded atomic.Int64
	waited    time.Duration
}

func startWatch(c config, rooms []room, run string) (*liveWatch, error) {
	nc, err := nats.Connect(c.nats, nats.Name("chatim-corebench"), nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	w := &liveWatch{nc: nc, live: openloop.NewLive(c.plan.WarmupShots()), rooms: len(rooms)}
	for _, r := range rooms {
		subject := c.liveRoot + "." + c.tenant + ".*." + r.id + ".>"
		sub, err := nc.Subscribe(subject, func(m *nats.Msg) { w.observe(run, m) })
		if err == nil {
			err = sub.SetPendingLimits(-1, -1)
		}
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("subscribe %s: %w", subject, err)
		}
	}
	if err := nc.FlushTimeout(flushTimeout); err != nil {
		nc.Close()
		return nil, fmt.Errorf("confirm live subscriptions: %w", err)
	}
	return w, nil
}

func (w *liveWatch) observe(run string, m *nats.Msg) {
	received := time.Now()
	var ev chatimv1.Event
	if err := proto.Unmarshal(m.Data, &ev); err != nil {
		w.undecoded.Add(1)
		return
	}
	if shot, ok := openloop.ShotOf(run, ev.GetMessageCreated().GetMessage().GetCid()); ok {
		w.live.Observe(shot, ev.GetTs().AsTime(), received)
	}
}

func (w *liveWatch) await(ctx context.Context, grace time.Duration) {
	start := time.Now()
	defer func() { w.waited = time.Since(start) }()
	tick := time.NewTicker(awaitPoll)
	defer tick.Stop()
	for !w.live.Complete() && time.Since(start) < grace {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *liveWatch) report(out io.Writer) {
	s := w.live.Summary()
	fmt.Fprintf(out, "live events on %d watched rooms: received=%d of acked=%d missing=%d duplicates=%d undecoded=%d (waited %v after the last send returned)\n",
		w.rooms, s.Received, s.Expected, max(s.Expected-s.Received, 0), s.Duplicates, w.undecoded.Load(), w.waited.Round(time.Millisecond))
	fmt.Fprintf(out, "live lag, receive time minus event ts (ts = seq assignment on the core, before the insert; one host clock): %v\n", s.Lag)
}

func (w *liveWatch) close() { w.nc.Close() }
