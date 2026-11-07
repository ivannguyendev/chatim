package app

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

const mediaSubsystem = "media"

func wireMedia(cfg config.Config, cl *clients, indexes *mongoindex.Health, chat *mediaChatNode, log *slog.Logger) (*mediaapp.App, error) {
	if !cfg.Media.Enabled {
		return nil, nil
	}
	app, err := mediaapp.New(cfg.Media, mediaapp.Deps{Mongo: cl.mongo.Database(cfg.MongoDB), Log: log.With("app", mediaSubsystem), Indexes: indexes, Chat: chat})
	if err != nil {
		return nil, fmt.Errorf("wire media app: %w", err)
	}
	return app, nil
}

func (a *node) attachMedia(app *mediaapp.App) []mediaapp.Probe {
	if app == nil {
		mediaapp.RegisterDisabled(a.grpc)
		return nil
	}
	app.Register(a.grpc)
	a.watches = liveWatchers{a.watches, app.Watch()}
	a.setups = append(a.setups, app)
	return app.Probes()
}

func mediaSources(probes []mediaapp.Probe) []metrics.Source {
	out := make([]metrics.Source, len(probes))
	for i, p := range probes {
		out[i] = metrics.Source{Subsystem: mediaSubsystem, Name: p.Name, Help: p.Help, Labels: p.Labels, Gauge: p.Gauge, Read: p.Read}
	}
	return out
}
