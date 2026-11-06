package mediaapp_test

import (
	"errors"
	"log/slog"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
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
	return mediaapp.Deps{Mongo: client.Database("chatim"), Log: slog.New(slog.DiscardHandler)}
}

func valid() mediaapp.Config {
	c := mediaapp.DefaultConfig()
	c.Endpoint, c.PublicEndpoint, c.Region = "http://127.0.0.1:1", "http://127.0.0.1:1", "us-east-1"
	c.AccessKey, c.SecretKey, c.ProcessingBucket, c.CompletedBucket = "GK1", "secret", "p", "c"
	return c
}

func TestNewNeedsMongoAndALogger(t *testing.T) {
	if _, err := mediaapp.New(valid(), mediaapp.Deps{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
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
	cc := a.ClientConfig()
	if cc.Enabled || cc.MaxFileBytes != 4<<30 || cc.MaxParallelUploads != 4 {
		t.Fatalf("client config %+v", cc)
	}
	if got := len(a.Probes()); got != 1+2+18 {
		t.Fatalf("probes = %d", got)
	}
	if mediaapp.Indexes().Collections[0].Name != "media_uploads" {
		t.Fatal("index catalog misses media_uploads")
	}
}
