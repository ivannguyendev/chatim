package actor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	cidCacheSize    = 4096
	cidCacheTTL     = 10 * time.Minute
	memberCacheSize = 1024
	seedSize        = store.MaxPageLimit
	maxRequeues     = 3
	findBackoff     = 5 * time.Millisecond
	maxFindBackoff  = 200 * time.Millisecond

	reservationMargin = time.Second

	ActiveMarkEvery = 5 * time.Second
)

var (
	errStarted       = errors.New("router already started")
	errNotRunning    = fmt.Errorf("router not accepting messages: %w", domain.ErrRetryLater)
	errStopped       = fmt.Errorf("room actor stopped: %w", domain.ErrRetryLater)
	errRetired       = fmt.Errorf("room actor retired after its slot moved: %w", domain.ErrRetryLater)
	errUnavailable   = fmt.Errorf("room store unavailable: %w", domain.ErrRetryLater)
	errUnconfirmed   = fmt.Errorf("message write unconfirmed: %w", domain.ErrRetryLater)
	errSeqContention = fmt.Errorf("sequence taken too many times: %w", domain.ErrRetryLater)
	errCIDElsewhere  = fmt.Errorf("cid in flight on another core: %w", domain.ErrRetryLater)
	errCIDUnsettled  = fmt.Errorf("cid reservation of an abandoned write still held: %w", domain.ErrRetryLater)
	errWriteNotSent  = fmt.Errorf("message write not sent before its deadline too many times: %w", domain.ErrRetryLater)
	errOutOfTime     = fmt.Errorf("message write would outlive its cid reservation: %w", domain.ErrRetryLater)
	errGroupExpired  = fmt.Errorf("write group deadline passed before submit: %w", domain.ErrRetryLater)
	errMailboxFull   = fmt.Errorf("room mailbox full: %w", domain.ErrBusy)
	errTooManyRooms  = fmt.Errorf("too many active rooms: %w", domain.ErrBusy)
)

type Submitter interface {
	Submit(ctx context.Context, g flush.Group) error
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
	Skip(room uint64, pts []uint64) error
}

type ActivityMarker interface {
	MarkActive(ctx context.Context, room uint64) error
}

type CIDRegistry interface {
	Reserve(ctx context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error)
	Commit(ctx context.Context, entries []dedupe.Entry) error
	Abort(ctx context.Context, keys []dedupe.Key) error
}

type SendCmd struct {
	Tenant, User string
	Room, Thread uint64
	CID, Text    string
}

type Ack struct {
	Seq, Pts  uint64
	CreatedAt time.Time
}

type Config struct {
	Mailbox        int
	Idle           time.Duration
	MaxGroup       int
	MaxActors      int
	GroupDeadline  time.Duration
	ReservationTTL time.Duration
}

func (c Config) validate() error {
	switch {
	case c.Mailbox <= 0 || c.Idle <= 0 || c.MaxGroup <= 0 || c.MaxActors <= 0 || c.GroupDeadline <= 0:
		return fmt.Errorf("%w: actor config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.ReservationTTL <= c.GroupDeadline+reservationMargin:
		return fmt.Errorf("%w: reservation ttl %v must exceed the group deadline %v plus %v", apperr.ErrInvalidArgument, c.ReservationTTL, c.GroupDeadline, reservationMargin)
	default:
		return nil
	}
}

func (c SendCmd) validate() error {
	if c.Room == 0 {
		return fmt.Errorf("%w: room", apperr.ErrInvalidArgument)
	}
	for _, err := range []error{
		domain.ValidTenant(c.Tenant),
		domain.ValidUser(c.User),
		domain.ValidCID(c.CID),
		domain.ValidateThread(c.Thread),
		domain.ValidateText(c.Text),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

func ackOf(m domain.Message) Ack {
	return Ack{Seq: m.Seq, Pts: m.Pts, CreatedAt: m.CreatedAt}
}

type reply struct {
	ack Ack
	err error
}

type request struct {
	cmd       SendCmd
	republish bool
	from      uint64
	reply     chan reply
}

func newRequest(c SendCmd) *request {
	return &request{cmd: c, reply: make(chan reply, 1)}
}

func newRecovery(room, from uint64) *request {
	return &request{cmd: SendCmd{Room: room}, republish: true, from: from, reply: make(chan reply, 1)}
}

func (q *request) answer(ack Ack, err error) {
	q.reply <- reply{ack: ack, err: err}
}
