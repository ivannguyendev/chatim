package app

import (
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

func partURLLimiter(cfg config.Config, shared *redis.Client, log *slog.Logger) (*ratelimit.Limiter, error) {
	var rdb redis.Scripter
	if shared != nil {
		rdb = shared
	} else {
		log.Warn("GetPartURLs limits count on this node only: SHARED_REDIS_ADDR is empty")
	}
	return ratelimit.New(rdb, ratelimit.Config{
		Command: "GetPartURLs", User: cfg.PartURLLimits.User, UserIP: cfg.PartURLLimits.UserIP, Device: cfg.PartURLLimits.Device,
		LocalUsers: cfg.UserLimiters,
	})
}

func limiterSources(l *ratelimit.Limiter) []metrics.Source {
	out := []metrics.Source{{
		Name: "command_rate_limit_fallbacks_total", Help: "Command limit checks counted on this node because the shared Redis did not answer.",
		Read: func() float64 { return float64(l.Fallbacks()) },
	}}
	for _, c := range l.Limited() {
		out = append(out, metrics.Source{
			Name: "command_rate_limited_total", Help: "Calls refused by a per-command limit, by command and tier (user, user_ip, device).",
			Labels: map[string]string{"command": c.Command, "tier": c.Tier}, Read: func() float64 { return float64(c.Read()) },
		})
	}
	return out
}
