package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	TierUser   = "user"
	TierUserIP = "user_ip"
	TierDevice = "device"
)

var countScript = redis.NewScript(`
local out = {}
for i, key in ipairs(KEYS) do
  local n = redis.call('INCR', key)
  if n == 1 then redis.call('PEXPIRE', key, ARGV[i]) end
  out[i] = n
end
return out
`)

type Limit struct {
	Count  int
	Window time.Duration
}

type Config struct {
	Command string
	User    Limit
	UserIP  Limit
	Device  Limit
}

type Key struct {
	Tenant, User, IP, Device string
}

type Decision struct {
	Allowed bool
	Tier    string
}

type tier struct {
	name  string
	limit Limit
	key   func(Key) string
	hits  atomic.Uint64
}

type Limiter struct {
	rdb     redis.Scripter
	command string
	tiers   [3]*tier
	errors  atomic.Uint64
	now     func() time.Time
}

func New(rdb redis.Scripter, c Config) (*Limiter, error) {
	if rdb == nil || c.Command == "" {
		return nil, errors.New("ratelimit: needs redis and a command name")
	}
	for _, l := range []Limit{c.User, c.UserIP, c.Device} {
		if l.Count < 1 || l.Window < time.Millisecond {
			return nil, fmt.Errorf("ratelimit: limit %d per %s must be at least 1 per 1ms", l.Count, l.Window)
		}
	}
	return &Limiter{rdb: rdb, command: c.Command, now: time.Now, tiers: [3]*tier{
		{name: TierUser, limit: c.User, key: func(k Key) string { return k.Tenant + ":" + k.User }},
		{name: TierUserIP, limit: c.UserIP, key: func(k Key) string { return k.Tenant + ":" + k.User + ":" + k.IP }},
		{name: TierDevice, limit: c.Device, key: func(k Key) string { return k.Tenant + ":" + k.User + ":" + k.IP + ":" + k.Device }},
	}}, nil
}

func (l *Limiter) Allow(ctx context.Context, k Key) (Decision, error) {
	now := l.now().UnixMilli()
	keys := make([]string, len(l.tiers))
	windows := make([]any, len(l.tiers))
	for i, t := range l.tiers {
		window := t.limit.Window.Milliseconds()
		keys[i] = "chatim:ratelimit:" + l.command + ":" + t.name + ":" + t.key(k) + ":" + strconv.FormatInt(now/window, 10)
		windows[i] = window
	}
	counts, err := countScript.Run(ctx, l.rdb, keys, windows...).Int64Slice()
	if err != nil || len(counts) != len(l.tiers) {
		l.errors.Add(1)
		return Decision{Allowed: true}, fmt.Errorf("ratelimit %s: %w", l.command, errors.Join(err, errShortReply(len(counts))))
	}
	for i := len(l.tiers) - 1; i >= 0; i-- {
		if counts[i] > int64(l.tiers[i].limit.Count) {
			l.tiers[i].hits.Add(1)
			return Decision{Tier: l.tiers[i].name}, nil
		}
	}
	return Decision{Allowed: true}, nil
}

func errShortReply(n int) error {
	if n == 3 {
		return nil
	}
	return fmt.Errorf("script returned %d counts", n)
}

type Count struct {
	Command, Tier string
	Read          func() uint64
}

func (l *Limiter) Limited() []Count {
	out := make([]Count, len(l.tiers))
	for i, t := range l.tiers {
		out[i] = Count{Command: l.command, Tier: t.name, Read: t.hits.Load}
	}
	return out
}

func (l *Limiter) Errors() uint64 { return l.errors.Load() }
