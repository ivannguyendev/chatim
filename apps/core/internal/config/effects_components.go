package config

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
)

const defaultMemberCountCheckDelay = 5 * time.Second

func (p *parser) workerConfig(c *Config) {
	c.Effects = effects.Config{
		Partitions: c.Work.Partitions,
		FetchBatch: p.count("WORK_FETCH_BATCH", effects.DefaultFetchBatch),
		FetchWait:  p.span("WORK_FETCH_WAIT", effects.DefaultFetchWait),
		RetryDelay: p.span("WORK_RETRY_DELAY", effects.DefaultRetryDelay),
		Drain:      p.span("WORK_DRAIN", effects.DefaultDrain),
		Poll:       c.Slot.Tick,
	}
	c.ReactionCountDelay = p.span("REACTION_COUNT_DELAY", effects.DefaultCountDelay)
	c.MemberCountCheckDelay = p.span("MEMBER_COUNT_CHECK_DELAY", defaultMemberCountCheckDelay)
}
