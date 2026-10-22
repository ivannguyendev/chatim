package feed

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/time/rate"
)

const catchUpAge = 5 * time.Second

type receiver struct {
	turn    chan struct{}
	lastSeq uint64
	ctx     context.Context
	root    string
	publish bool
	fan     *fanout
	limiter *rate.Limiter
	stats   *streamStats
	unknown *atomic.Uint64
	dropped *atomic.Uint64
}

func newReceiver(ctx context.Context, root string, publish bool, fan *fanout, perSecond int, stats *streamStats, unknown, dropped *atomic.Uint64) *receiver {
	return &receiver{turn: make(chan struct{}, 1), ctx: ctx, root: root, publish: publish, fan: fan, limiter: replayLimiter(perSecond), stats: stats, unknown: unknown, dropped: dropped}
}

func replayLimiter(perSecond int) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(perSecond), max(1, perSecond/10))
}

func (r *receiver) handle(msg jetstream.Msg) {
	r.turn <- struct{}{}
	defer func() { <-r.turn }()
	meta, err := msg.Metadata()
	if err != nil {
		r.unknown.Add(1)
		return
	}
	if meta.Sequence.Stream <= r.lastSeq {
		r.stats.stale.Add(1)
		return
	}
	r.lastSeq = meta.Sequence.Stream
	defer func() { r.stats.observe(meta, time.Now()) }()
	env, key, ok := r.envelope(msg, meta)
	if !ok {
		r.unknown.Add(1)
		return
	}
	if time.Since(meta.Timestamp) > catchUpAge && r.limiter.Wait(r.ctx) != nil {
		r.dropped.Add(1)
		return
	}
	if !r.fan.send(key, job{publish: r.publish, env: env}) {
		r.dropped.Add(1)
	}
}

func (r *receiver) envelope(msg jetstream.Msg, meta *jetstream.MsgMetadata) (Envelope, string, bool) {
	parse := parseEvent
	if r.publish {
		parse = parsePublish
	}
	env, ok := parse(r.root, msg.Subject())
	if !ok {
		return Envelope{}, "", false
	}
	if h := msg.Headers(); h != nil {
		env.ID = h.Get(jetstream.MsgIDHeader)
	}
	env.Seq = meta.Sequence.Stream
	env.StoredAt = meta.Timestamp
	env.Data = msg.Data()
	if !r.publish {
		return env, env.Room, true
	}
	if env.ID != "" {
		return env, env.ID, true
	}
	return env, strconv.FormatUint(env.Seq, 10), true
}
