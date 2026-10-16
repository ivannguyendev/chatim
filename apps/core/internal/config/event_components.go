package config

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
)

const (
	defaultMemberCountCheckDelay  = 5 * time.Second
	defaultMessageCountCheckDelay = 5 * time.Second
	sweepLookbackSlack            = 10 * time.Second
)

func (p *parser) eventConfig(c *Config) {
	c.StateClear = stateclear.Config{
		Flush: p.span("STATE_CLEAR_FLUSH", stateclear.DefaultFlush),
		Batch: p.count("STATE_CLEAR_BATCH", stateclear.DefaultBatch),
		Queue: p.count("STATE_CLEAR_QUEUE", stateclear.DefaultQueue),
	}
	c.Sweep = sweep.Config{
		SubjectRoot: c.Stream.SubjectRoot,
		Interval:    p.span("SWEEP_INTERVAL", sweep.DefaultInterval),
		MinAge:      p.span("SWEEP_MIN_AGE", sweep.DefaultMinAge),
		Lookback:    p.span("SWEEP_LOOKBACK", sweep.DefaultLookback),
		Batch:       p.count("SWEEP_BATCH", sweep.DefaultBatch),
		Rate:        p.count("SWEEP_RATE", sweep.DefaultRate),
		Tick:        c.Slot.Tick,
		AckTimeout:  c.Publish.AckTimeout,
	}
	c.Projection = projection.Config{
		Stream:      c.Stream.Name,
		SubjectRoot: c.Stream.SubjectRoot,
		FetchBatch:  p.count("PROJECTION_FETCH_BATCH", projection.DefaultFetchBatch),
		FetchWait:   p.span("PROJECTION_FETCH_WAIT", projection.DefaultFetchWait),
	}
	c.MemberCountCheckDelay = p.span("MEMBER_COUNT_CHECK_DELAY", defaultMemberCountCheckDelay)
	c.MessageCountCheckDelay = p.span("MESSAGE_COUNT_CHECK_DELAY", defaultMessageCountCheckDelay)
}
