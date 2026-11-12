package mediaapp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/config"
	"github.com/ivannguyendev/chatim/modules/media/internal/consumer"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
	"github.com/ivannguyendev/chatim/modules/media/internal/gallery"
	"github.com/ivannguyendev/chatim/modules/media/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/readiness"
	"github.com/ivannguyendev/chatim/modules/media/internal/roomfiles"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Config = config.Config

func DefaultConfig() Config { return config.Defaults() }

type Deps struct {
	Mongo       *mongo.Database
	JS          jetstream.JetStream
	Stream      string
	SubjectRoot string
	Log         *slog.Logger
	Indexes     mongoindex.Reporter
	Chat        ChatReader
}

type Watcher interface {
	Start(ctx context.Context) error
	Stop()
}

type ClientConfig struct {
	Enabled            bool
	MaxFileBytes       int64
	MaxParallelUploads int
	Thumbnails         bool
}

type App struct {
	cfg      Config
	deps     Deps
	objects  *objectstore.S3
	uploads  *upload.Service
	attach   *attach.Service
	consumer *consumer.Consumer
	watch    *readiness.Watch
	server   chatimv1.MediaServiceServer
}

func New(cfg Config, deps Deps) (*App, error) {
	if deps.Mongo == nil || deps.JS == nil || deps.Stream == "" || deps.SubjectRoot == "" || deps.Log == nil || deps.Chat == nil {
		return nil, fmt.Errorf("%w: media app needs mongo, jetstream with the event stream and subject root, a logger and the chat reader", apperr.ErrInvalidArgument)
	}
	a := &App{cfg: cfg, deps: deps}
	if err := a.assemble(); err != nil {
		deps.Log.Error("media is not ready: bad configuration", "err", err)
		a.watch = readiness.New(func(context.Context) error { return err }, cfg.ReadyCheckInterval, deps.Log)
		a.server = grpcsrv.Refusing{Err: domain.ErrNotReady}
	}
	return a, nil
}

func (a *App) assemble() error {
	if err := a.cfg.Validate(); err != nil {
		return err
	}
	objects, err := objectstore.New(objectstore.Config{
		Endpoint: a.cfg.Endpoint, PublicEndpoint: a.cfg.PublicEndpoint, Region: a.cfg.Region,
		AccessKey: a.cfg.AccessKey, SecretKey: a.cfg.SecretKey,
		Bucket: a.cfg.ProcessingBucket, CompletedBucket: a.cfg.CompletedBucket, Copy: a.cfg.CopyLimits(),
	})
	if err != nil {
		return err
	}
	a.objects = objects
	a.watch = readiness.New(objects.CheckBuckets, a.cfg.ReadyCheckInterval, a.deps.Log)
	uploads := mongostore.NewUploads(a.deps.Mongo)
	a.uploads, err = upload.New(upload.Config{Limits: a.cfg.Limits(), PartURLTTL: a.cfg.PartSignatureTTL}, upload.Deps{
		Uploads: uploads, Objects: objects, Ready: a.watch.Ready,
	})
	if err != nil {
		return err
	}
	downloads, err := download.New(download.Config{SignatureTTL: a.cfg.DownloadSignatureTTL, Thumbnails: a.cfg.ThumbnailRule()}, download.Deps{
		Chat: a.deps.Chat, Uploads: uploads, Objects: objects, Ready: a.watch.Ready,
	})
	if err != nil {
		return err
	}
	files, err := gallery.New(gallery.Config{
		SignatureTTL: a.cfg.DownloadSignatureTTL, Retention: a.cfg.ProcessingRetention, Thumbnails: a.cfg.ThumbnailRule(),
	}, gallery.Deps{Uploads: uploads, Objects: objects, Ready: a.watch.Ready})
	if err != nil {
		return err
	}
	rooms, err := roomfiles.New(roomfiles.Config{SignatureTTL: a.cfg.DownloadSignatureTTL, Thumbnails: a.cfg.ThumbnailRule()}, roomfiles.Deps{
		Chat: a.deps.Chat, Files: mongostore.NewMessageFiles(a.deps.Mongo), Uploads: uploads, Objects: objects, Ready: a.watch.Ready,
	})
	if err != nil {
		return err
	}
	if err := a.assembleConsumer(uploads); err != nil {
		return err
	}
	a.server = grpcsrv.New(a.uploads, downloads, files, rooms)
	return nil
}

func (a *App) Register(r grpc.ServiceRegistrar) { chatimv1.RegisterMediaServiceServer(r, a.server) }

func RegisterDisabled(r grpc.ServiceRegistrar) {
	chatimv1.RegisterMediaServiceServer(r, grpcsrv.Refusing{Err: domain.ErrDisabled})
}

func (a *App) Ready() bool { return a.watch.Ready() }

func (a *App) Watch() Watcher { return storageWatch{Watch: a.watch, objects: a.objects} }

type storageWatch struct {
	*readiness.Watch
	objects *objectstore.S3
}

func (w storageWatch) Stop() {
	w.Watch.Stop()
	if w.objects != nil {
		w.objects.CloseIdle()
	}
}

func (a *App) ClientConfig() ClientConfig {
	return ClientConfig{
		Enabled: a.Ready(), MaxFileBytes: a.cfg.MaxFileBytes, MaxParallelUploads: a.cfg.MaxParallelUploads, Thumbnails: a.cfg.Thumbnails,
	}
}

func Indexes() mongoindex.Catalog { return mongostore.Indexes() }
