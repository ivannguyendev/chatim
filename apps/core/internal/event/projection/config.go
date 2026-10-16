package projection

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultFetchBatch         = 256
	DefaultFetchWait          = 500 * time.Millisecond
	DefaultActivityFetchBatch = 1024
	DefaultActivityFetchWait  = time.Second
	AckWait                   = 30 * time.Second
	MaxAckPending             = 4096
)

type Config struct {
	Stream             string
	SubjectRoot        string
	FetchBatch         int
	FetchWait          time.Duration
	ActivityFetchBatch int
	ActivityFetchWait  time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	c.ActivityFetchBatch = cmp.Or(c.ActivityFetchBatch, DefaultActivityFetchBatch)
	c.ActivityFetchWait = cmp.Or(c.ActivityFetchWait, DefaultActivityFetchWait)
	return c
}

func (c Config) fetchFor(projection string) Config {
	if projection == RoomActivityName {
		c.FetchBatch, c.FetchWait = c.ActivityFetchBatch, c.ActivityFetchWait
	}
	return c
}

func (c Config) validate() error {
	switch {
	case !subjectToken(c.Stream) || !subjectToken(c.SubjectRoot):
		return fmt.Errorf("%w: projection stream %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Stream, c.SubjectRoot)
	case c.FetchBatch <= 0 || c.FetchBatch > MaxAckPending || c.FetchWait <= 0:
		return fmt.Errorf("%w: projection fetch batch %d must be 1..%d and fetch wait %v positive", apperr.ErrInvalidArgument, c.FetchBatch, MaxAckPending, c.FetchWait)
	case c.ActivityFetchBatch <= 0 || c.ActivityFetchBatch > MaxAckPending || c.ActivityFetchWait <= 0:
		return fmt.Errorf("%w: room activity fetch batch %d must be 1..%d and fetch wait %v positive", apperr.ErrInvalidArgument, c.ActivityFetchBatch, MaxAckPending, c.ActivityFetchWait)
	default:
		return nil
	}
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
