package projection

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	leadStartedMsg = "projection started on the leader slot"
	leadLostMsg    = "projection lost the leader slot; fetching stopped"
)

type Owner interface {
	Owns(slot uint16) bool
}

type Option func(*Consumer)

func WithLeader(owner Owner, slot uint16, tick time.Duration) Option {
	return func(c *Consumer) { c.lead = leader{owner: owner, slot: slot, tick: tick} }
}

type leader struct {
	owner Owner
	slot  uint16
	tick  time.Duration
	held  bool
}

func (l leader) validate() error {
	if l.owner != nil && l.tick <= 0 {
		return fmt.Errorf("%w: projection leader tick %v must be positive", apperr.ErrInvalidArgument, l.tick)
	}
	return nil
}

func (c *Consumer) leading(ctx context.Context) bool {
	if c.lead.owner == nil {
		return true
	}
	now := c.lead.owner.Owns(c.lead.slot)
	switch {
	case now && !c.lead.held:
		c.log.InfoContext(ctx, leadStartedMsg, "slot", c.lead.slot)
	case !now && c.lead.held:
		c.log.InfoContext(ctx, leadLostMsg, "slot", c.lead.slot)
	}
	c.lead.held = now
	return now
}
