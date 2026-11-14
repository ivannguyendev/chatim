package itest

import (
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/ivannguyendev/chatim/apps/chat/internal/app"
	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/core/admin"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	itRooms      = 4
	itSenders    = 16
	itAckedFirst = 500
	itStopLimit  = 10 * time.Second
)

type running struct {
	done chan struct{}
	err  error
}

func TestRealInfraStopUnderLoadKeepsEveryAckedMessage(t *testing.T) {
	it := realInfra(t)
	cfg := it.chatConfig(t, map[string]string{"APP_DRAIN_DELAY": "200ms"})
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithCancel(context.Background())
	chat := &running{done: make(chan struct{})}
	go func() {
		defer close(chat.done)
		chat.err = app.Run(ctx, cfg, logger)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-chat.done:
		case <-time.After(itCleanupLimit):
			t.Errorf("run still running %v after cleanup cancelled it", itCleanupLimit)
		}
	})
	awaitReady(t, cfg, chat)

	client := dialChat(t, cfg)
	rooms := make([]string, itRooms)
	for i := range rooms {
		rooms[i] = createRoom(t, client)
	}
	l := startLoad(client, rooms, itSenders)
	l.awaitAcks(t, itAckedFirst)

	stopAt := time.Now()
	cancel()
	select {
	case <-chat.done:
	case <-time.After(itStopLimit):
		l.stop()
		t.Fatalf("run did not return within %v of the stop signal", itStopLimit)
	}
	took := time.Since(stopAt)
	l.stop()
	if chat.err != nil {
		t.Fatalf("run = %v, want a clean stop", chat.err)
	}
	acks := l.acked()
	t.Logf("stopped in %v after %d acked sends, %d sends after the stop signal", took, len(acks), l.afterStop())
	verifyAcked(t, it, cfg, acks)
}

func awaitReady(t *testing.T, cfg config.Config, chat *running) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-chat.done:
			t.Fatalf("run returned before ready: %v", chat.err)
		default:
		}
		ctx, cancel := context.WithTimeout(t.Context(), itProbeTimeout)
		err := admin.Probe(ctx, cfg.AdminAddr)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("chat not ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dialChat(t *testing.T, cfg config.Config) chatimv1.ChatServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(cfg.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return chatimv1.NewChatServiceClient(conn)
}

func createRoom(t *testing.T, client chatimv1.ChatServiceClient) string {
	t.Helper()
	resp, err := client.CreateRoom(caller(t.Context()), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "load", Members: []string{itUser, "bob"}, RequestId: rand.Text(),
	})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	return resp.GetRoom().GetId()
}

func verifyAcked(t *testing.T, it *itInfra, cfg config.Config, acks []sent) {
	t.Helper()
	st := mongostore.New(it.mongo.Database(cfg.MongoDB), mongostore.Options{})
	events := it.streamIDs(t, cfg.Stream.Name)
	byRoom := map[uint64][]sent{}
	for _, a := range acks {
		byRoom[a.room] = append(byRoom[a.room], a)
	}
	stored := 0
	for room, list := range byRoom {
		msgs := storedMessages(t, st, room)
		stored += len(msgs)
		bySeq := make(map[uint64]domain.Message, len(msgs))
		copies := make(map[string]int, len(msgs))
		for _, m := range msgs {
			bySeq[m.Seq] = m
			copies[m.CID]++
		}
		for cid, n := range copies {
			if n > 1 {
				t.Errorf("room %d stores cid %s %d times", room, cid, n)
			}
		}
		for _, a := range list {
			if m, ok := bySeq[a.seq]; !ok || m.CID != a.cid || m.From != itUser {
				t.Errorf("acked %s in room %d at seq %d is not stored there (found %+v)", a.cid, room, a.seq, m)
			}
			if events[pbconv.MessageEventID(room, 0, a.seq)] == 0 {
				t.Errorf("acked %s in room %d has no event for seq %d", a.cid, room, a.seq)
			}
		}
	}
	t.Logf("verified %d acked of %d stored messages against %d stream events", len(acks), stored, len(events))
}

func storedMessages(t *testing.T, st *mongostore.Store, room uint64) []domain.Message {
	t.Helper()
	var out []domain.Message
	var after uint64
	for {
		page, err := st.Page(t.Context(), store.PageQuery{Room: room, Anchor: store.After, Seq: after, Limit: store.MaxPageLimit})
		if err != nil {
			t.Fatalf("page room %d after %d: %v", room, after, err)
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, page...)
		after = page[len(page)-1].Seq
	}
}
