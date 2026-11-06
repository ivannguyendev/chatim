package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/ratelimit"
)

type PartURLLimits struct {
	User   ratelimit.Limit
	UserIP ratelimit.Limit
	Device ratelimit.Limit
}

func (p *parser) partURLLimits() PartURLLimits {
	return PartURLLimits{
		User:   p.limit("GW_PART_URLS_USER_LIMIT", ratelimit.Limit{Count: 2, Window: time.Second}),
		UserIP: p.limit("GW_PART_URLS_USER_IP_LIMIT", ratelimit.Limit{Count: 1, Window: time.Second}),
		Device: p.limit("GW_PART_URLS_DEVICE_LIMIT", ratelimit.Limit{Count: 1, Window: 5 * time.Second}),
	}
}

func (p *parser) limit(key string, def ratelimit.Limit) ratelimit.Limit {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	count, window, ok := strings.Cut(raw, "/")
	n, err := strconv.Atoi(count)
	d, derr := time.ParseDuration(window)
	if !ok || err != nil || derr != nil || n < 1 || d < time.Millisecond {
		p.fail(fmt.Errorf("%s=%q: want COUNT/DURATION such as 1/5s", key, raw))
		return def
	}
	return ratelimit.Limit{Count: n, Window: d}
}

func (p *parser) prefixes(key string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range list(key) {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			p.fail(fmt.Errorf("%s: %q is not a CIDR: %w", key, item, err))
			continue
		}
		out = append(out, prefix.Masked())
	}
	return out
}

func (l PartURLLimits) logValue() slog.Attr {
	format := func(x ratelimit.Limit) string { return fmt.Sprintf("%d/%s", x.Count, x.Window) }
	return slog.Group("part_url_limits", slog.String("user", format(l.User)), slog.String("user_ip", format(l.UserIP)), slog.String("device", format(l.Device)))
}
