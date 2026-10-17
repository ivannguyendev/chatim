package actor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	cidCacheSize    = 4096
	cidCacheTTL     = 10 * time.Minute
	memberCacheSize = 1024
	seedSize        = store.MaxPageLimit

	reservationMargin = time.Second
)

var (
	errStarted       = errors.New("router already started")
	errNotRunning    = fmt.Errorf("router not accepting messages: %w", domain.ErrRetryLater)
	errStopped       = fmt.Errorf("room actor stopped: %w", domain.ErrRetryLater)
	errRetired       = fmt.Errorf("room actor retired after its slot moved: %w", domain.ErrRetryLater)
	errUnavailable   = fmt.Errorf("room store unavailable: %w", domain.ErrRetryLater)
	errUnconfirmed   = fmt.Errorf("message write unconfirmed: %w", domain.ErrRetryLater)
	errSeqContention = fmt.Errorf("sequence taken by another write: %w", domain.ErrRetryLater)
	errCIDElsewhere  = fmt.Errorf("cid in flight on another core: %w", domain.ErrRetryLater)
	errCIDUnsettled  = fmt.Errorf("cid reservation of an abandoned write still held: %w", domain.ErrRetryLater)
	errGroupExpired  = fmt.Errorf("write group deadline passed before the insert was sent: %w", domain.ErrRetryLater)
	errMailboxFull   = fmt.Errorf("room mailbox full: %w", domain.ErrBusy)
	errTooManyRooms  = fmt.Errorf("too many active rooms: %w", domain.ErrBusy)
)

type Submitter interface {
	Submit(ctx context.Context, g flush.Group) error
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
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
	ReplyTo      *domain.ReplyRef
	Forward      *domain.ForwardRef
	Mentions     []domain.MentionTarget
	MentionAll   bool
	Kind         domain.Kind
	SkipUnread   bool
	Meta         []byte
}

type Ack struct {
	Seq       uint64
	CreatedAt time.Time
}

type Config struct {
	Mailbox        int
	Idle           time.Duration
	MaxGroup       int
	MaxActors      int
	GroupDeadline  time.Duration
	ReservationTTL time.Duration
	MentionTargets int
}

func (c Config) Validate() error {
	switch {
	case c.Mailbox <= 0 || c.Idle <= 0 || c.MaxGroup <= 0 || c.MaxActors <= 0 || c.GroupDeadline <= 0 || c.MentionTargets <= 0:
		return fmt.Errorf("%w: actor config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.ReservationTTL <= c.GroupDeadline+reservationMargin:
		return fmt.Errorf("%w: reservation ttl %v must exceed the group deadline %v plus %v", apperr.ErrInvalidArgument, c.ReservationTTL, c.GroupDeadline, reservationMargin)
	default:
		return nil
	}
}

func (c SendCmd) normalize(mentionLimit int) (SendCmd, error) {
	if c.Room == 0 {
		return SendCmd{}, fmt.Errorf("%w: room", apperr.ErrInvalidArgument)
	}
	for _, err := range []error{
		domain.ValidTenant(c.Tenant),
		domain.ValidUser(c.User),
		domain.ValidCID(c.CID),
		domain.ValidateThread(c.Thread),
		domain.ValidateText(c.Text),
		domain.ValidateReply(c.ReplyTo),
		domain.ValidateMeta(c.Meta),
	} {
		if err != nil {
			return SendCmd{}, err
		}
	}
	mentions, err := domain.ValidateMentions(c.Mentions, mentionLimit)
	if err != nil {
		return SendCmd{}, err
	}
	c.Mentions = mentions
	if c.Kind, err = sendKind(c.Kind); err != nil {
		return SendCmd{}, err
	}
	if err := domain.ValidateSystemMessage(c.message()); err != nil {
		return SendCmd{}, err
	}
	return c, nil
}

func sendKind(k domain.Kind) (domain.Kind, error) {
	switch k {
	case 0, domain.KindText:
		return domain.KindText, nil
	case domain.KindSystem:
		return k, nil
	default:
		return 0, fmt.Errorf("%w: kind", apperr.ErrInvalidArgument)
	}
}

func (c SendCmd) message() domain.Message {
	return domain.Message{
		Room:       c.Room,
		Tenant:     c.Tenant,
		From:       c.User,
		Kind:       c.Kind,
		Text:       c.Text,
		CID:        c.CID,
		ReplyTo:    c.ReplyTo,
		Forward:    c.Forward,
		Mentions:   c.Mentions,
		MentionAll: c.MentionAll,
		SkipUnread: c.SkipUnread,
		Meta:       c.Meta,
	}
}

func ackOf(m domain.Message) Ack {
	return Ack{Seq: m.Seq, CreatedAt: m.CreatedAt}
}

type reply struct {
	ack Ack
	err error
}

type request struct {
	cmd   SendCmd
	reply chan reply
}

func newRequest(c SendCmd) *request {
	return &request{cmd: c, reply: make(chan reply, 1)}
}

func (q *request) answer(ack Ack, err error) {
	q.reply <- reply{ack: ack, err: err}
}
