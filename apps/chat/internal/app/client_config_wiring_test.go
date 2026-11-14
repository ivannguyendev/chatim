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
	cfg.Interactions.PinLimit = 7
	cfg.Members.MemberBatch = 9
	off := clientConfig(cfg, nil)()
	if off.GetRoom().GetMaxPins() != 7 || off.GetRoom().GetMaxMembersPerCall() != 9 {
		t.Fatalf("room limits %v", off.GetRoom())
	}
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
	deps := mediaapp.Deps{Mongo: client.Database("chatim"), JS: idleJetStream{}, Stream: "CHATIM_EVT", SubjectRoot: "evt", Log: quiet, Chat: &mediaChatNode{}}
	mc := mediaapp.DefaultConfig()
	mc.Endpoint, mc.PublicEndpoint, mc.Region = "http://127.0.0.1:1", "http://127.0.0.1:1", "us-east-1"
	mc.AccessKey, mc.SecretKey, mc.ProcessingBucket, mc.CompletedBucket = "k", "s", "p", "c"
	media, err := mediaapp.New(mc, deps)
	if err != nil {
		t.Fatal(err)
	}
	notReady := clientConfig(cfg, media)().GetMedia()
	if notReady.GetEnabled() || notReady.GetMaxFileBytes() != 4<<30 || notReady.GetMaxParallelUploads() != 4 || notReady.GetThumbnails() {
		t.Fatalf("media before storage is ready %v", notReady)
	}
	mc.Thumbnails = true
	thumbs, err := mediaapp.New(mc, deps)
	if err != nil {
		t.Fatal(err)
	}
	withMedia := clientConfig(cfg, thumbs)()
	if got := withMedia.GetMedia(); !got.GetThumbnails() {
		t.Fatalf("media with thumbnails on %v", got)
	}
	if withMedia.GetRoom().GetMaxPins() != 7 || withMedia.GetRoom().GetMaxMembersPerCall() != 9 {
		t.Fatalf("room limits with media %v", withMedia.GetRoom())
	}
}
