package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/memberwatch"
)

type memberWatcher interface {
	Start(ctx context.Context) error
	Stop()
}

func wireMemberWatch(cfg config.Config, nc *nats.Conn, router *actor.Router, log *slog.Logger) (*memberwatch.Watch, error) {
	w, err := memberwatch.New(nc, cfg.Stream.LiveRoot, router.ForgetMembers, log)
	if err != nil {
		return nil, fmt.Errorf("wire member watch: %w", err)
	}
	return w, nil
}

func memberWatchSources(forgets, malformed func() uint64) []metrics.Source {
	return []metrics.Source{
		{Name: "member_cache_forgets_total", Help: "Rooms whose cached members this core forgot after a member_removed or member_role_changed event.", Read: func() float64 { return float64(forgets()) }},
		{Name: "member_watch_malformed_total", Help: "Member events skipped because their subject carried no valid room id.", Read: func() float64 { return float64(malformed()) }},
	}
}
