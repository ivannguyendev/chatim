package projection

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultFetchBatch = 1024
	DefaultFetchWait  = time.Second
	AckWait           = 30 * time.Second
	MaxFetchBatch     = 8192
	MaxFetchWait      = time.Second
)

type Config struct {
	Stream      string
	SubjectRoot string
	FetchBatch  int
	FetchWait   time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) MaxAckPending() int { return 2 * c.withDefaults().FetchBatch }

func (c Config) withDefaults() Config {
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	return c
}

func (c Config) validate() error {
	switch {
	case !subjectToken(c.Stream) || !subjectToken(c.SubjectRoot):
		return fmt.Errorf("%w: projection stream %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Stream, c.SubjectRoot)
	case c.FetchBatch <= 0 || c.FetchBatch > MaxFetchBatch || c.FetchWait <= 0 || c.FetchWait > MaxFetchWait:
		return fmt.Errorf("%w: projection fetch batch %d must be 1..%d and fetch wait %v in (0, %v]", apperr.ErrInvalidArgument, c.FetchBatch, MaxFetchBatch, c.FetchWait, MaxFetchWait)
	default:
		return nil
	}
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
