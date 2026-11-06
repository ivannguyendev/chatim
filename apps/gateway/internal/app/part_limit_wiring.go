package app

import (
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
)

func partURLLimiter(cfg config.Config, shared *redis.Client, log *slog.Logger) (*ratelimit.Limiter, error) {
	if shared == nil {
		log.Warn("GetPartURLs limits are off: GW_SHARED_REDIS_ADDR is empty")
		return nil, nil
	}
	return ratelimit.New(shared, ratelimit.Config{
		Command: "GetPartURLs", User: cfg.PartURLLimits.User, UserIP: cfg.PartURLLimits.UserIP, Device: cfg.PartURLLimits.Device,
	})
}

func limiterSources(l *ratelimit.Limiter) []metrics.Source {
	if l == nil {
		return nil
	}
	out := []metrics.Source{{
		Name: "command_rate_limit_errors_total", Help: "Calls let through because the shared Redis holding command limits did not answer.",
		Read: func() float64 { return float64(l.Errors()) },
	}}
	for _, c := range l.Limited() {
		out = append(out, metrics.Source{
			Name: "command_rate_limited_total", Help: "Calls refused by a per-command limit, by command and tier (user, user_ip, device).",
			Labels: map[string]string{"command": c.Command, "tier": c.Tier}, Read: func() float64 { return float64(c.Read()) },
		})
	}
	return out
}
