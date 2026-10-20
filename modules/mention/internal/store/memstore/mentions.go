package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
)

var _ store.Mentions = (*Mentions)(nil)

type mentionID struct {
	key    domain.MessageKey
	target domain.Target
}

type Mentions struct {
	mu   sync.Mutex
	docs map[mentionID]domain.Mention
}

func NewMentions() *Mentions {
	return &Mentions{docs: make(map[mentionID]domain.Mention)}
}

func (s *Mentions) Apply(ctx context.Context, plan domain.Plan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	plan = plan.InMillis()
	now := time.Now().UTC().Truncate(time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range plan.Live {
		s.put(plan, t, domain.StateLive, now)
	}
	for _, t := range plan.Retired {
		s.put(plan, t, domain.StateRetired, now)
	}
	return nil
}

func (s *Mentions) put(plan domain.Plan, t domain.Target, state domain.State, now time.Time) {
	id := mentionID{key: plan.Key, target: t.Identity()}
	next := domain.Mention{
		Key: plan.Key, Tenant: plan.Tenant, Target: id.target, Sender: plan.Sender,
		State: state, MessageVer: plan.Ver, CreatedAt: t.At.UTC(), UpdatedAt: now,
	}
	cur, ok := s.docs[id]
	if ok {
		if cur.Tenant != plan.Tenant || cur.MessageVer > plan.Ver {
			return
		}
		if t.At.IsZero() {
			next.CreatedAt = cur.CreatedAt
		}
		if cur.State == next.State && cur.MessageVer == next.MessageVer && cur.CreatedAt.Equal(next.CreatedAt) && cur.Sender == next.Sender {
			return
		}
	}
	s.docs[id] = next
}

func (s *Mentions) Of(ctx context.Context, tenant string, key domain.MessageKey) ([]domain.Mention, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMentionsOf(tenant, key); err != nil {
		return nil, err
	}
	return s.listWhere(-1, func(d domain.Mention) bool { return d.Key == key && d.Tenant == tenant }), nil
}
