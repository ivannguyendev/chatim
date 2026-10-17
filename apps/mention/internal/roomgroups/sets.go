package roomgroups

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultEntries = 4096
	RebuildTimeout = 30 * time.Second
)

type Config struct {
	SyncMax int
	TTL     time.Duration
	Rebuild time.Duration
	Entries int
}

type BuildFunc func(ctx context.Context, tenant string, room uint64) (map[string]struct{}, error)

type Set struct {
	Groups   map[string]struct{}
	Filtered bool
}

type Sets struct {
	background
	cache  *Cache
	build  BuildFunc
	cfg    Config
	log    *slog.Logger
	flight singleflight.Group
}

func NewSets(cfg Config, build BuildFunc, log *slog.Logger) (*Sets, error) {
	switch {
	case cfg.SyncMax < 1 || cfg.TTL <= 0 || cfg.Rebuild <= 0:
		return nil, fmt.Errorf("%w: room groups sync max %d, ttl %v and rebuild %v must be positive", apperr.ErrInvalidArgument, cfg.SyncMax, cfg.TTL, cfg.Rebuild)
	case build == nil || log == nil:
		return nil, fmt.Errorf("%w: room groups need a build func and a logger", apperr.ErrInvalidArgument)
	}
	cfg.Entries = cmp.Or(cfg.Entries, DefaultEntries)
	cache, err := NewCache(cfg.Entries)
	if err != nil {
		return nil, err
	}
	return &Sets{background: newBackground(), cache: cache, build: build, cfg: cfg, log: log}, nil
}

func (s *Sets) Of(ctx context.Context, tenant string, room uint64, members int64) (Set, error) {
	if members <= int64(s.cfg.SyncMax) {
		return s.small(ctx, tenant, room)
	}
	return s.large(tenant, room), nil
}

func (s *Sets) Forget(room uint64) { s.cache.Forget(room) }

func (s *Sets) Len() int { return s.cache.Len() }

func (s *Sets) small(ctx context.Context, tenant string, room uint64) (Set, error) {
	if e, ok := s.cache.Get(room); ok && e.Small && time.Since(e.BuiltAt) < s.cfg.TTL {
		return Set{Groups: e.Groups, Filtered: true}, nil
	}
	v, err, _ := s.flight.Do(strconv.FormatUint(room, 10), func() (any, error) {
		mark := s.cache.Mark()
		groups, err := s.build(ctx, tenant, room)
		if err != nil {
			return nil, err
		}
		s.cache.Put(mark, room, Entry{Groups: groups, BuiltAt: time.Now(), Small: true})
		return groups, nil
	})
	if err != nil {
		return Set{}, err
	}
	return Set{Groups: v.(map[string]struct{}), Filtered: true}, nil
}
