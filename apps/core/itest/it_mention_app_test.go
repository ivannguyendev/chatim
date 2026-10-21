package itest

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type mentionDocView struct {
	Target string `bson:"target"`
	State  int64  `bson:"state"`
}

func dialMention(t *testing.T, cfg config.Config) chatimv1.MentionServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(cfg.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return chatimv1.NewMentionServiceClient(conn)
}

func tagGroups(t *testing.T, client chatimv1.MentionServiceClient, members map[string][]string, groups ...string) {
	t.Helper()
	ctx := callerAs(t.Context(), itUser)
	items := make([]*chatimv1.GroupItem, len(groups))
	for i, g := range groups {
		items[i] = &chatimv1.GroupItem{Id: g, Name: g, Active: true, Ver: 1}
	}
	if _, err := client.SetGroups(ctx, &chatimv1.SetGroupsRequest{Items: items}); err != nil {
		t.Fatalf("SetGroups: %v", err)
	}
	var people []*chatimv1.UserGroupsItem
	for user, of := range members {
		people = append(people, &chatimv1.UserGroupsItem{User: user, Groups: of, Ver: 1})
	}
	if _, err := client.SetUserGroups(ctx, &chatimv1.SetUserGroupsRequest{Items: people}); err != nil {
		t.Fatalf("SetUserGroups: %v", err)
	}
}

func subscribeNotify(t *testing.T, it *itInfra, cfg config.Config) <-chan *nats.Msg {
	t.Helper()
	notices := make(chan *nats.Msg, 256)
	sub, err := it.nc.ChanSubscribe(cfg.Mention.WsPublishRoot+".>", notices)
	if err != nil {
		t.Fatalf("subscribe notify: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return notices
}

func awaitNotified(t *testing.T, notices <-chan *nats.Msg, users ...string) map[string]*chatimv1.Notification {
	t.Helper()
	got := make(map[string]*chatimv1.Notification, len(users))
	deadline := time.After(itLiveLimit)
	for len(got) < len(users) {
		select {
		case m := <-notices:
			d := &chatimv1.WsPublish{}
			if err := proto.Unmarshal(m.Data, d); err != nil {
				t.Fatalf("decode ws publish packet on %s: %v", m.Subject, err)
			}
			n := d.GetNotification()
			if n.GetMentionNotified() == nil || n.GetUser() != "" || d.GetKind() != "mention" {
				t.Fatalf("ws publish packet %s carries %v, want a mention_notified naming no user", d.GetId(), n)
			}
			for _, user := range d.GetTargets().GetUsers() {
				got[user] = n
			}
		case <-deadline:
			t.Fatalf("notifications for %v did not all arrive within %v; got %v", users, itLiveLimit, got)
		}
	}
	return got
}

func storedMentions(t *testing.T, it *itInfra, cfg config.Config, room, seq uint64) func() ([]mentionDocView, error) {
	return func() ([]mentionDocView, error) {
		ctx, cancel := context.WithTimeout(t.Context(), itCleanupLimit)
		defer cancel()
		cur, err := it.mongo.Database(cfg.MongoDB).Collection("mentions").Find(ctx, bson.D{{Key: "room_id", Value: int64(room)}, {Key: "seq", Value: int64(seq)}})
		if err != nil {
			return nil, err
		}
		var docs []mentionDocView
		return docs, cur.All(ctx, &docs)
	}
}
