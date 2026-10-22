package feed

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/time/rate"
)

const catchUpAge = 5 * time.Second

type source struct {
	root      string
	publish   bool
	perSecond int
	maxAge    time.Duration
}

type receiver struct {
	source
	turn     chan struct{}
	ctx      context.Context
	fan      *fanout
	limiter  *rate.Limiter
	stats    *streamStats
	unknown  *atomic.Uint64
	dropped  *atomic.Uint64
	onJump   func(*receiver, JumpReason)
	lastSeq  uint64
	epoch    uint64
	jumping  bool
	lost     bool
	created  time.Time
	progress time.Time
	ccMu     sync.Mutex
	cc       jetstream.ConsumeContext
}

func newReceiver(ctx context.Context, src source, fan *fanout, stats *streamStats, unknown, dropped *atomic.Uint64, onJump func(*receiver, JumpReason)) *receiver {
	return &receiver{
		source: src, turn: make(chan struct{}, 1), ctx: ctx, fan: fan, limiter: replayLimiter(src.perSecond),
		stats: stats, unknown: unknown, dropped: dropped, onJump: onJump, progress: time.Now(),
	}
}

func replayLimiter(perSecond int) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(perSecond), max(1, perSecond/10))
}

func (r *receiver) handlerFor(epoch uint64) jetstream.MessageHandler {
	return func(msg jetstream.Msg) { r.handle(epoch, msg) }
}

func (r *receiver) handle(epoch uint64, msg jetstream.Msg) {
	r.turn <- struct{}{}
	defer func() { <-r.turn }()
	if epoch != r.epoch || r.jumping {
		return
	}
	meta, err := msg.Metadata()
	if err != nil {
		r.unknown.Add(1)
		return
	}
	if meta.Sequence.Stream <= r.lastSeq {
		r.stats.stale.Add(1)
		return
	}
	now := time.Now()
	age := now.Sub(meta.Timestamp)
	if age > r.maxAge {
		r.jumping = true
		r.onJump(r, JumpLagTooOld)
		return
	}
	r.lastSeq = meta.Sequence.Stream
	r.progress = now
	defer func() { r.stats.observe(meta, time.Now()) }()
	env, key, ok := r.envelope(msg, meta)
	if !ok {
		r.unknown.Add(1)
		return
	}
	env.CatchingUp = age > catchUpAge
	if env.CatchingUp && r.limiter.Wait(r.ctx) != nil {
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

func (r *receiver) take(ctx context.Context) bool {
	select {
	case r.turn <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *receiver) give() { <-r.turn }

func (r *receiver) restart(ctx context.Context, pos Position) (uint64, bool) {
	if !r.take(ctx) {
		return 0, false
	}
	defer r.give()
	r.epoch++
	r.lastSeq, r.created, r.progress = pos.Last, pos.Created, time.Now()
	r.jumping, r.lost = false, false
	return r.epoch, true
}

func (r *receiver) lose(ctx context.Context) {
	if !r.take(ctx) {
		return
	}
	defer r.give()
	r.jumping, r.lost = true, true
}

func (r *receiver) verdict(pos Position, err error, now time.Time) (JumpReason, bool) {
	switch {
	case r.lost:
		r.lost = false
		return JumpConsumerLost, true
	case r.jumping || err != nil:
		return 0, false
	case !pos.Created.Equal(r.created) || pos.Last < r.lastSeq:
		r.jumping = true
		return JumpConsumerLost, true
	case pos.Last > r.lastSeq && now.Sub(r.progress) > r.maxAge:
		r.jumping = true
		return JumpLagTooOld, true
	}
	return 0, false
}

func (r *receiver) attach(cc jetstream.ConsumeContext) {
	r.ccMu.Lock()
	defer r.ccMu.Unlock()
	r.cc = cc
}

func (r *receiver) detach() jetstream.ConsumeContext {
	r.ccMu.Lock()
	defer r.ccMu.Unlock()
	cc := r.cc
	r.cc = nil
	if cc != nil {
		cc.Stop()
	}
	return cc
}
