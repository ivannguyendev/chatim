package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MessageCreatedName = "msg_created"
	DefaultDelay       = 5 * time.Second
	DefaultRoomCache   = 65536
)

type MessageCreatedDeps struct {
	Marks    Marks
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type MessageCreatedConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
}

type MessageCreated struct {
	deps        MessageCreatedDeps
	cfg         MessageCreatedConfig
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewMessageCreated(deps MessageCreatedDeps, cfg MessageCreatedConfig) (*MessageCreated, error) {
	if deps.Marks == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: msg_created needs marks, messages, rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: msg_created config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, cfg)
	}
	return &MessageCreated{deps: deps, cfg: cfg, types: newRoomTypes(deps.Rooms, cfg.RoomCache)}, nil
}

func (e *MessageCreated) Effect() Effect {
	return Effect{Name: MessageCreatedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *MessageCreated) Republished() uint64 { return e.republished.Load() }

func (e *MessageCreated) Dropped() uint64 { return e.dropped.Load() }

func (e *MessageCreated) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	keys := make([]store.MsgKey, len(recs))
	for i, r := range recs {
		keys[i] = store.MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
	}
	var pending []pendingAck
	for _, idx := range e.unmarkedByRoom(ctx, keys) {
		pending = e.publishRoom(ctx, keys, idx, errs, pending)
	}
	awaitAcks(ctx, pending, errs, countAll(&e.republished))
	return errs
}

func (e *MessageCreated) unmarkedByRoom(ctx context.Context, keys []store.MsgKey) [][]int {
	acked, err := e.deps.Marks.Acked(ctx, keys)
	if err != nil || len(acked) != len(keys) {
		acked = make([]bool, len(keys))
	}
	at := map[uint64]int{}
	var out [][]int
	for i, k := range keys {
		if acked[i] {
			continue
		}
		g, ok := at[k.Room]
		if !ok {
			g = len(out)
			at[k.Room] = g
			out = append(out, nil)
		}
		out[g] = append(out[g], i)
	}
	return out
}

func (e *MessageCreated) publishRoom(ctx context.Context, keys []store.MsgKey, idx []int, errs []error, pending []pendingAck) []pendingAck {
	room := keys[idx[0]].Room
	typ, err := e.types.get(ctx, room)
	var found map[store.MsgKey]domain.Message
	if err == nil {
		found, err = e.find(ctx, room, keys, idx)
	}
	switch {
	case undeliverable(err):
		for range idx {
			e.dropped.Add(1)
		}
		return pending
	case err != nil:
		for _, i := range idx {
			errs[i] = err
		}
		return pending
	}
	for _, i := range idx {
		m, ok := found[keys[i]]
		if !ok {
			e.dropped.Add(1)
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, room, pbconv.MessageCreated(typ, m))
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	return pending
}

func (e *MessageCreated) find(ctx context.Context, room uint64, keys []store.MsgKey, idx []int) (map[store.MsgKey]domain.Message, error) {
	want := make([]store.MsgKey, len(idx))
	for j, i := range idx {
		want[j] = keys[i]
	}
	msgs, err := e.deps.Messages.Find(ctx, room, want)
	if err != nil {
		return nil, err
	}
	found := make(map[store.MsgKey]domain.Message, len(msgs))
	for _, m := range msgs {
		found[store.KeyOf(m)] = m
	}
	return found, nil
}
