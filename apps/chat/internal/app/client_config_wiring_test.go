package app

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
)

func TestClientConfigFollowsSettingsAndMediaReadiness(t *testing.T) {
	var cfg config.Config
	cfg.Messages.MentionTargets = 20
	cfg.Mention.Enabled = true
	off := clientConfig(cfg, nil)()
	if off.GetMessage().GetMaxTextBytes() != 16384 || off.GetMessage().GetMaxMetaBytes() != 8192 || off.GetMessage().GetMaxMentions() != 20 || off.GetMessage().GetMaxAttachments() != 10 {
		t.Fatalf("message limits %v", off.GetMessage())
	}
	if !off.GetMention().GetEnabled() || off.GetMedia().GetEnabled() {
		t.Fatalf("features %v %v", off.GetMention(), off.GetMedia())
	}
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mc := mediaapp.DefaultConfig()
	mc.Endpoint, mc.PublicEndpoint, mc.Region = "http://127.0.0.1:1", "http://127.0.0.1:1", "us-east-1"
	mc.AccessKey, mc.SecretKey, mc.ProcessingBucket, mc.CompletedBucket = "k", "s", "p", "c"
	media, err := mediaapp.New(mc, mediaapp.Deps{Mongo: client.Database("chatim"), Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	notReady := clientConfig(cfg, media)().GetMedia()
	if notReady.GetEnabled() || notReady.GetMaxFileBytes() != 4<<30 || notReady.GetMaxParallelUploads() != 4 {
		t.Fatalf("media before storage is ready %v", notReady)
	}
}
