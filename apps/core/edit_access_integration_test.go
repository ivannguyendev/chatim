package main

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func messageChange(m *nats.Msg) bool {
	tokens := strings.Split(m.Subject, ".")
	return len(tokens) > 2 && tokens[2] == "message" && strings.Contains(m.Header.Get(jetstream.MsgIDHeader), "-v")
}

func awaitMemberEvents(t *testing.T, live <-chan *nats.Msg, liveRoot, roomID string, want map[string]string) {
	t.Helper()
	deadline := time.After(itLiveLimit)
	for len(want) > 0 {
		select {
		case m := <-live:
			id := m.Header.Get(jetstream.MsgIDHeader)
			if messageChange(m) {
				t.Fatalf("message change event %s on %s arrived, want none for hide and clear", id, m.Subject)
			}
			kind, ok := want[id]
			if !ok {
				continue
			}
			if subject := liveRoot + "." + itTenant + ".member." + roomID + ".evt." + kind; m.Subject != subject {
				t.Fatalf("event %s on %s, want %s", id, m.Subject, subject)
			}
			delete(want, id)
		case <-deadline:
			t.Fatalf("member events %v did not arrive within %v", want, itLiveLimit)
		}
	}
}

func assertNoMessageChanges(t *testing.T, live <-chan *nats.Msg, wait time.Duration) {
	t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case m := <-live:
			if messageChange(m) {
				t.Fatalf("message change event %s on %s arrived, want none for hide and clear", m.Header.Get(jetstream.MsgIDHeader), m.Subject)
			}
		case <-timeout:
			return
		}
	}
}

func hiddenOnly(m *chatimv1.Message) bool {
	return m.GetHidden() && m.GetText() == "" && m.GetSeq() != 0
}

func TestRealInfraConcurrentEditsOnOneBaseHaveOneWinner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "edit-d", "base")

	texts := []string{"left", "right"}
	errs := make([]error, len(texts))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, text := range texts {
		wg.Go(func() {
			<-start
			_, errs[i] = client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: text})
		})
	}
	close(start)
	wg.Wait()
	winner := slices.IndexFunc(errs, func(err error) bool { return err == nil })
	if winner < 0 || status.Code(errs[1-winner]) != codes.FailedPrecondition {
		t.Fatalf("concurrent edits returned %v, want one success and one FailedPrecondition", errs)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; m.GetText() != texts[winner] || m.GetVer() != 1 {
		t.Fatalf("history shows %v, want the winner %q at version 1", m, texts[winner])
	}

	bobSeq := sendAs(t, client, "bob", roomID, "edit-d-bob", "from bob")
	if _, err := client.DeleteMessage(callerAs(t.Context(), "bob"), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq, BaseVer: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bob deletes alice's message = %v, want PermissionDenied", err)
	}
	if _, err := client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: bobSeq, Text: "not mine"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the owner edits bob's message = %v, want PermissionDenied", err)
	}
	if _, err := client.DeleteMessage(caller(t.Context()), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: bobSeq}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the owner deletes bob's message = %v, want PermissionDenied with the default policy", err)
	}
	if _, err := client.DeleteMessage(callerAs(t.Context(), "bob"), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: bobSeq}); err != nil {
		t.Fatalf("bob deletes his own message: %v", err)
	}
}

func TestRealInfraHideAndClearApplyOnlyToTheReaderAndAnnounceIt(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	live := subscribeLive(t, it, core.cfg, roomID)
	for i := range 3 {
		sendAs(t, client, itUser, roomID, "hide-"+strconv.Itoa(i+1), "visible "+strconv.Itoa(i+1))
	}

	bob := callerAs(t.Context(), "bob")
	if _, err := client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: roomID, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || got[1].GetHidden() || got[2].GetHidden() || !hiddenOnly(got[3]) {
		t.Fatalf("bob's history = %v, want only seq 3 hidden, every seq kept", got)
	}
	resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID})
	if err != nil || resp.GetClearedAt() == nil {
		t.Fatalf("ClearHistory = %v, %v; want a cleared_at mark", resp, err)
	}
	mark := resp.GetClearedAt().AsTime()
	sendAs(t, client, itUser, roomID, "hide-4", "visible 4")
	for seq, m := range historyAs(t, client, itUser, roomID) {
		if m.GetHidden() || m.GetText() != "visible "+strconv.FormatUint(seq, 10) {
			t.Fatalf("alice sees seq %d as %v, want it visible", seq, m)
		}
	}
	created := historyAs(t, client, itUser, roomID)[4].GetCreatedAt().AsTime()
	for seq, m := range historyAs(t, client, "bob", roomID) {
		if cleared := seq < 4 || !created.After(mark); cleared != hiddenOnly(m) {
			t.Fatalf("bob sees seq %d as %v after clearing at %v (seq 4 sent at %v), want hidden=%v", seq, m, mark, created, cleared)
		}
	}
	if again, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil || again.GetClearedAt().AsTime().Before(mark) {
		t.Fatalf("ClearHistory again = %v, %v; want the mark never to go back from %v", again, err, mark)
	}
	room, err := strconv.ParseUint(roomID, 10, 64)
	if err != nil {
		t.Fatalf("room id %q: %v", roomID, err)
	}
	awaitMemberEvents(t, live, core.cfg.Stream.LiveRoot, roomID, map[string]string{
		pbconv.HiddenEventID(room, "bob", 0, 3):  "message_hidden",
		pbconv.ClearedEventID(room, "bob", mark): "history_cleared",
	})
	assertNoMessageChanges(t, live, 3*time.Second)
}
