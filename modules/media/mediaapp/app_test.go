package mediaapp_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc/metadata"

	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func deps(t *testing.T) mediaapp.Deps {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(t.Context()) })
	return mediaapp.Deps{
		Mongo: client.Database("chatim"), JS: &lookups{}, Stream: "CHATIM_EVT", SubjectRoot: "evt",
		Log: slog.New(slog.DiscardHandler), Chat: noChat{},
	}
}

type lookups struct {
	jetstream.JetStream
	mu    sync.Mutex
	asked int
}

func (l *lookups) Consumer(context.Context, string, string) (jetstream.Consumer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked++
	return nil, errors.New("no consumer in unit tests")
}

func (l *lookups) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.asked
}

func withCaller(ctx context.Context) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(chatrpc.TenantHeader, "acme", chatrpc.UserHeader, "an"))
}

type noChat struct{}

func (noChat) VisibleAttachments(context.Context, string, string, []mediaapp.MessageRef) ([]mediaapp.MessageFiles, error) {
	return nil, nil
}

func (noChat) MessageFiles(context.Context, mediaapp.MessageRef) (mediaapp.MessageState, error) {
	return mediaapp.MessageState{}, nil
}

func valid() mediaapp.Config {
	c := mediaapp.DefaultConfig()
	c.Endpoint, c.PublicEndpoint, c.Region = "http://127.0.0.1:1", "http://127.0.0.1:1", "us-east-1"
	c.AccessKey, c.SecretKey, c.ProcessingBucket, c.CompletedBucket = "GK1", "secret", "p", "c"
	return c
}

func TestNewNeedsMongoALoggerAndChat(t *testing.T) {
	if _, err := mediaapp.New(valid(), mediaapp.Deps{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
	for name, drop := range map[string]func(*mediaapp.Deps){
		"chat":         func(d *mediaapp.Deps) { d.Chat = nil },
		"jetstream":    func(d *mediaapp.Deps) { d.JS = nil },
		"stream":       func(d *mediaapp.Deps) { d.Stream = "" },
		"subject root": func(d *mediaapp.Deps) { d.SubjectRoot = "" },
	} {
		d := deps(t)
		drop(&d)
		if _, err := mediaapp.New(valid(), d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("no %s err = %v", name, err)
		}
	}
}

func TestBadConfigLeavesMediaNotReadyInsteadOfFailing(t *testing.T) {
	c := valid()
	c.Region = ""
	a, err := mediaapp.New(c, deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Ready() || a.ClientConfig().Enabled {
		t.Fatal("bad config reported ready")
	}
	_, err = a.Server().StartUpload(t.Context(), &chatimv1.StartUploadRequest{})
	if !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("start = %v", err)
	}
	w := a.Watch()
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.Stop()
	probes := a.Probes()
	if len(probes) != 1 || probes[0].Name != "ready" || probes[0].Read() != 0 {
		t.Fatalf("probes %+v", probes)
	}
}

func TestValidConfigServesUploadsOnceStorageIsReady(t *testing.T) {
	a, err := mediaapp.New(valid(), deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if a.Ready() {
		t.Fatal("ready before the first bucket check")
	}
	a.Watch().Stop()
	cc := a.ClientConfig()
	if cc.Enabled || cc.MaxFileBytes != 4<<30 || cc.MaxParallelUploads != 4 || cc.Thumbnails {
		t.Fatalf("client config %+v", cc)
	}
	c := valid()
	c.Thumbnails = true
	if on, err := mediaapp.New(c, deps(t)); err != nil || !on.ClientConfig().Thumbnails {
		t.Fatalf("thumbnails on: %v", err)
	}
	if _, err := a.Server().GetAttachmentURLs(withCaller(t.Context()), &chatimv1.GetAttachmentURLsRequest{Messages: []*chatimv1.MessageRef{{RoomId: "7", Seq: 1}}}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("attachment urls before ready = %v", err)
	}
	if got := len(a.Probes()); got != 1+2+21+2+6+3 {
		t.Fatalf("probes = %d", got)
	}
	if mediaapp.Indexes().Collections[0].Name != "media_uploads" {
		t.Fatal("index catalog misses media_uploads")
	}
}
