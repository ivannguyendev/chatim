package app

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/chatrpc"
	"github.com/ivannguyendev/chatim/pkg/core/grpcserver"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestIndexCatalogAddsMediaUploadsOnlyWhenEnabled(t *testing.T) {
	var cfg config.Config
	if slices.Contains(indexCatalog(cfg).Names(), "media_uploads") {
		t.Fatal("media_uploads in the catalog while media is off")
	}
	cfg.Media.Enabled = true
	if !slices.Contains(indexCatalog(cfg).Names(), "media_uploads") {
		t.Fatal("media_uploads missing while media is on")
	}
}

func mediaClient(t *testing.T, a *node) chatimv1.MediaServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = a.grpc.ServeListener(ctx, lis)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	cc, err := grpc.NewClient("passthrough:///chat", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return chatimv1.NewMediaServiceClient(cc)
}

func TestDisabledMediaStillServesAClearRefusal(t *testing.T) {
	a := &node{grpc: grpcserver.New(grpcserver.Config{RequestDeadline: time.Second}, quiet)}
	if probes := a.attachMedia(nil); probes != nil || len(a.setups) != 0 {
		t.Fatalf("attach(nil) probes %v setups %v", probes, a.setups)
	}
	client := mediaClient(t, a)
	_, err := client.StartUpload(withCaller(t.Context()), &chatimv1.StartUploadRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled start = %v", err)
	}
	_, err = client.GetAttachmentURLs(withCaller(t.Context()), &chatimv1.GetAttachmentURLsRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled attachment urls = %v", err)
	}
}

func TestEnabledMediaRefusesWhileStorageIsNotReady(t *testing.T) {
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	cfg := mediaapp.DefaultConfig()
	cfg.Endpoint, cfg.PublicEndpoint, cfg.Region = "http://127.0.0.1:1", "http://127.0.0.1:1", "us-east-1"
	cfg.AccessKey, cfg.SecretKey, cfg.ProcessingBucket, cfg.CompletedBucket = "k", "s", "p", "c"
	app, err := mediaapp.New(cfg, mediaapp.Deps{Mongo: client.Database("chatim"), Log: quiet, Chat: &mediaChatNode{}})
	if err != nil {
		t.Fatal(err)
	}
	a := &node{grpc: grpcserver.New(grpcserver.Config{RequestDeadline: time.Second}, quiet), watches: liveWatchers{&fakeWatch{}}}
	probes := a.attachMedia(app)
	if len(a.setups) != 1 || len(a.watches.(liveWatchers)) != 2 {
		t.Fatalf("setups %d watches %v", len(a.setups), a.watches)
	}
	if _, err := metrics.Handler(metricsNamespace, metricsSubsystem, mediaSources(probes)); err != nil {
		t.Fatalf("media metrics: %v", err)
	}
	_, err = mediaClient(t, a).StartUpload(withCaller(t.Context()), &chatimv1.StartUploadRequest{FileName: "a", ContentType: "a/b", Size: 1})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("not ready start = %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("call hung")
	}
}

func withCaller(ctx context.Context) context.Context {
	return chatrpc.WithCaller(ctx, "acme", "an")
}
