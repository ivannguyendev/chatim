package mediaapp

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/consumer"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/mongostore"
)

func (a *App) assembleConsumer(uploads *mongostore.Uploads) error {
	svc, err := attach.New(attach.Config{Thumbnails: a.cfg.ThumbnailRule()}, attach.Deps{
		Chat: a.deps.Chat, Uploads: uploads, Files: mongostore.NewMessageFiles(a.deps.Mongo),
		Avatars: mongostore.NewRoomAvatars(a.deps.Mongo), Objects: a.objects, Log: a.deps.Log,
	})
	if err != nil {
		return err
	}
	cfg := a.consumerConfig()
	c, err := consumer.New(cfg, consumer.Deps{
		Source: consumer.NewSource(a.deps.JS, cfg), Sync: svc, Stats: svc.Stats(), Ready: a.watch.Ready, Log: a.deps.Log,
	})
	if err != nil {
		return err
	}
	a.attach, a.consumer = svc, c
	return nil
}

func (a *App) consumerConfig() consumer.Config {
	return consumer.Config{
		Stream: a.deps.Stream, SubjectRoot: a.deps.SubjectRoot, MaxPending: a.cfg.MaxPending,
		AckWait: a.cfg.AckWait, SyncTimeout: a.cfg.SyncTimeout,
	}
}

func (a *App) Prepare(ctx context.Context) error {
	var err error
	if a.deps.Indexes == nil {
		err = mongostore.Bootstrap(ctx, a.deps.Mongo)
	} else {
		err = mongostore.Bootstrap(ctx, a.deps.Mongo, a.deps.Indexes)
	}
	if err != nil {
		return fmt.Errorf("bootstrap media collections: %w", err)
	}
	if a.consumer == nil {
		return nil
	}
	return consumer.EnsureConsumer(ctx, a.deps.JS, a.consumerConfig())
}

func (a *App) Run(ctx context.Context) error {
	if a.consumer == nil {
		<-ctx.Done()
		return nil
	}
	return a.consumer.Run(ctx)
}

func (a *App) Close(ctx context.Context) error {
	if a.consumer == nil {
		return nil
	}
	return a.consumer.Close(ctx)
}

func StopTimeout() time.Duration { return consumer.StopTimeout() }
