package memberwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	subjectTokens = 6
	roomToken     = 3
)

var watchedKinds = []string{"member_removed", "member_role_changed"}

var errStarted = errors.New("member watch already started")

type subscription interface {
	Unsubscribe() error
}

type subscriber interface {
	Subscribe(subject string, handle func(subject string)) (subscription, error)
}

type natsSubscriber struct {
	conn *nats.Conn
}

func (s natsSubscriber) Subscribe(subject string, handle func(string)) (subscription, error) {
	sub, err := s.conn.Subscribe(subject, func(m *nats.Msg) { handle(m.Subject) })
	if err != nil {
		return nil, err
	}
	return sub, nil
}

type Watch struct {
	bus       subscriber
	liveRoot  string
	forget    func(room uint64)
	log       *slog.Logger
	mu        sync.Mutex
	subs      []subscription
	forgets   atomic.Uint64
	malformed atomic.Uint64
}

func New(conn *nats.Conn, liveRoot string, forget func(room uint64), log *slog.Logger) (*Watch, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: member watch needs a nats connection", apperr.ErrInvalidArgument)
	}
	return newWatch(natsSubscriber{conn: conn}, liveRoot, forget, log)
}

func newWatch(bus subscriber, liveRoot string, forget func(room uint64), log *slog.Logger) (*Watch, error) {
	switch {
	case liveRoot == "" || strings.ContainsAny(liveRoot, ".*> \t\r\n"):
		return nil, fmt.Errorf("%w: member watch live root %q must be one subject token", apperr.ErrInvalidArgument, liveRoot)
	case forget == nil || log == nil:
		return nil, fmt.Errorf("%w: member watch needs a forget func and a logger", apperr.ErrInvalidArgument)
	}
	return &Watch{bus: bus, liveRoot: liveRoot, forget: forget, log: log}, nil
}

func (w *Watch) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.subs) > 0 {
		return errStarted
	}
	for _, kind := range watchedKinds {
		subject := w.liveRoot + ".*.member.*.evt." + kind
		sub, err := w.bus.Subscribe(subject, w.handle)
		if err != nil {
			w.unsubscribeAll(ctx)
			return fmt.Errorf("member watch subscribe %s: %w", subject, err)
		}
		w.subs = append(w.subs, sub)
	}
	w.log.InfoContext(ctx, "member watch started", "live_root", w.liveRoot)
	return nil
}

func (w *Watch) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unsubscribeAll(context.Background())
}

func (w *Watch) Forgets() uint64 { return w.forgets.Load() }

func (w *Watch) Malformed() uint64 { return w.malformed.Load() }

func (w *Watch) unsubscribeAll(ctx context.Context) {
	for _, sub := range w.subs {
		if err := sub.Unsubscribe(); err != nil {
			w.log.WarnContext(ctx, "member watch unsubscribe failed", "err", err)
		}
	}
	w.subs = nil
}

func (w *Watch) handle(subject string) {
	room, ok := roomOf(subject)
	if !ok {
		w.malformed.Add(1)
		return
	}
	w.forgets.Add(1)
	w.forget(room)
}

func roomOf(subject string) (uint64, bool) {
	tokens := strings.Split(subject, ".")
	if len(tokens) != subjectTokens {
		return 0, false
	}
	room, err := strconv.ParseUint(tokens[roomToken], 10, 64)
	return room, err == nil && room != 0
}
