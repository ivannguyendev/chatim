package mentionapp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/mention/internal/config"
	"github.com/ivannguyendev/chatim/apps/mention/internal/consumer"
	"github.com/ivannguyendev/chatim/apps/mention/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/mention/internal/roomgroups"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Config = config.Config

func DefaultConfig() Config { return config.Defaults() }

type Deps struct {
	Mongo    *mongo.Database
	JS       jetstream.JetStream
	NotifyJS jetstream.JetStream
	NATS     *nats.Conn
	Core     CoreReader
	Log      *slog.Logger
}

type App struct {
	cfg       Config
	deps      Deps
	bootstrap func(context.Context, *mongo.Database) error
	consumer  *consumer.Consumer
	sets      *roomgroups.Sets
	watch     *roomgroups.Watch
	service   *grpcsrv.Service
}

func New(cfg Config, deps Deps) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := deps.validate(); err != nil {
		return nil, err
	}
	a := &App{cfg: cfg.WithDefaults(), deps: deps, bootstrap: mongostore.Bootstrap}
	if err := a.assemble(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) StopTimeout() time.Duration { return a.cfg.StopTimeout() }

func (d Deps) validate() error {
	if d.Mongo == nil || d.JS == nil || d.NotifyJS == nil || d.NATS == nil || d.Core == nil || d.Log == nil {
		return fmt.Errorf("%w: mention app needs mongo, jetstream, notify jetstream, nats, a core reader and a logger", apperr.ErrInvalidArgument)
	}
	return nil
}
