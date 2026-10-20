package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/metrics"
)

const mentionSubsystem = "mention"

type preparer interface {
	Prepare(ctx context.Context) error
}

func wireMention(cfg config.Config, cl *clients, st *mongostore.Store, caches roomCaches, visible visibleReader, log *slog.Logger) (*mentionapp.App, error) {
	if !cfg.Mention.Enabled {
		return nil, nil
	}
	checker, err := access.NewChecker(st, access.DefaultPolicy{})
	if err != nil {
		return nil, fmt.Errorf("wire mention access checker: %w", err)
	}
	core := mentionCore{members: st, messages: st, infos: st, info: caches.info, visible: visible, access: checker}
	app, err := mentionapp.New(cfg.Mention, mentionapp.Deps{
		Mongo: cl.mongo.Database(cfg.MongoDB), JS: cl.js, NotifyJS: cl.notifyJS, NATS: cl.nats, Core: core, Log: log.With("app", mentionSubsystem),
	})
	if err != nil {
		return nil, fmt.Errorf("wire mention app: %w", err)
	}
	return app, nil
}

func (a *node) attachMention(app *mentionapp.App, watches liveWatchers) []mentionapp.Probe {
	if app == nil {
		a.watches = watches
		return nil
	}
	app.Register(a.grpc)
	watches = append(watches, app.Watch())
	a.watches, a.mention, a.setups = watches, app, append(a.setups, app)
	return app.Probes()
}

func mentionSources(probes []mentionapp.Probe) []metrics.Source {
	out := make([]metrics.Source, len(probes))
	for i, p := range probes {
		out[i] = metrics.Source{Subsystem: mentionSubsystem, Name: p.Name, Help: p.Help, Labels: p.Labels, Gauge: p.Gauge, Read: p.Read}
	}
	return out
}
