package dedupe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
)

var reserveScript = redis.NewScript(`
local res = {}
for i, k in ipairs(KEYS) do
  local cur = redis.pcall('GET', k)
  if type(cur) == 'table' then
    res[i] = ''
  elseif cur then
    res[i] = cur
  else
    redis.call('SET', k, ARGV[1], 'PX', ARGV[2])
    res[i] = 1
  end
end
return res`)

var abortScript = redis.NewScript(`
local n = 0
for _, k in ipairs(KEYS) do
  if redis.pcall('GET', k) == ARGV[1] then
    n = n + redis.call('DEL', k)
  end
end
return n`)

type Store struct {
	rdb   *redis.Client
	cfg   Config
	log   *slog.Logger
	guard *redisguard.Guard
	now   func() time.Time
}

func New(rdb *redis.Client, cfg Config, log *slog.Logger) (*Store, error) {
	if err := redisguard.CheckClient(rdb, "dedupe"); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	log = log.With("core", cfg.CoreID)
	s := &Store{rdb: rdb, cfg: cfg, log: log, now: time.Now}
	guard, err := redisguard.New(redisguard.Config{
		Name:      "cid dedupe",
		Timeout:   cfg.Timeout,
		Cooldown:  cfg.Cooldown,
		Skipped:   ErrDegraded,
		Degraded:  "cid dedupe degraded to the local cache",
		Recovered: "cid dedupe recovered",
		Now:       func() time.Time { return s.now() },
	}, log)
	if err != nil {
		return nil, err
	}
	s.guard = guard
	return s, nil
}

func (s *Store) Reserve(ctx context.Context, keys []Key) ([]Verdict, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	var out []Verdict
	err := s.guard.Do(ctx, "reserve", func(cctx context.Context) error {
		raw, err := reserveScript.Run(cctx, s.rdb, redisKeys(keys), pendingValue(s.cfg.CoreID), s.cfg.PendingTTL.Milliseconds()).Slice()
		if err != nil {
			return err
		}
		if len(raw) != len(keys) {
			return fmt.Errorf("reserve answered %d of %d keys", len(raw), len(keys))
		}
		out = make([]Verdict, len(keys))
		for i, v := range raw {
			out[i] = s.verdict(cctx, keys[i], v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Commit(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.guard.Do(ctx, "commit", func(cctx context.Context) error {
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			for _, e := range entries {
				p.Set(cctx, e.Key.String(), committedValue(e.Record), s.cfg.CommittedTTL)
			}
			return nil
		})
		return err
	})
}

func (s *Store) Abort(ctx context.Context, keys []Key) error {
	if len(keys) == 0 {
		return nil
	}
	return s.guard.Do(ctx, "abort", func(cctx context.Context) error {
		return abortScript.Run(cctx, s.rdb, redisKeys(keys), pendingValue(s.cfg.CoreID)).Err()
	})
}

func (s *Store) verdict(ctx context.Context, k Key, v any) Verdict {
	if n, ok := v.(int64); ok && n == 1 {
		return Verdict{Status: Reserved}
	}
	str, _ := v.(string)
	if out, ok := parseValue(str, s.cfg.CoreID); ok {
		return out
	}
	s.log.WarnContext(ctx, "malformed cid dedupe value treated as absent", "key", k.String(), "bytes", len(str))
	return Verdict{Status: Absent}
}

func redisKeys(keys []Key) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	return out
}

func (s *Store) Degraded() bool { return s.guard.Degraded() }
