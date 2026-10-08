package dedupe

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultBatchShards  = 4
	DefaultBatchMaxKeys = 256
	DefaultBatchQueue   = 4096
)

type BatchConfig struct {
	Shards  int
	MaxKeys int
	Queue   int
}

func (c BatchConfig) Validate() error { return c.withDefaults().validate() }

func (c BatchConfig) withDefaults() BatchConfig {
	c.Shards = cmp.Or(c.Shards, DefaultBatchShards)
	c.MaxKeys = cmp.Or(c.MaxKeys, DefaultBatchMaxKeys)
	c.Queue = cmp.Or(c.Queue, DefaultBatchQueue)
	return c
}

func (c BatchConfig) validate() error {
	switch {
	case c.Shards <= 0 || c.MaxKeys <= 0 || c.Queue <= 0:
		return fmt.Errorf("%w: cid batch config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: cid batch shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	default:
		return nil
	}
}

func (c BatchConfig) Connections() int { return 2 * c.withDefaults().Shards }
