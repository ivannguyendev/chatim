package mentionapp

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/ivannguyendev/chatim/modules/mention/internal/consumer"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Watcher interface {
	Start(ctx context.Context) error
	Stop()
}

func (a *App) Prepare(ctx context.Context) error {
	if err := a.bootstrap(ctx, a.deps.Mongo); err != nil {
		return fmt.Errorf("bootstrap mention collections: %w", err)
	}
	if err := notify.EnsureStream(ctx, a.deps.JS, a.cfg); err != nil {
		return err
	}
	return consumer.EnsureConsumer(ctx, a.deps.JS, a.cfg)
}

func (a *App) Run(ctx context.Context) error { return a.consumer.Run(ctx) }

func (a *App) Close(ctx context.Context) error {
	err := a.consumer.Close(ctx)
	a.sets.Close()
	return err
}

func (a *App) Register(r grpc.ServiceRegistrar) { chatimv1.RegisterMentionServiceServer(r, a.service) }

func (a *App) Watch() Watcher { return a.watch }
