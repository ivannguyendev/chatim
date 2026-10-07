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

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func assertNoChangeEvents(t *testing.T, live <-chan *nats.Msg, wait time.Duration) {
	t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case m := <-live:
			if id := m.Header.Get(jetstream.MsgIDHeader); strings.Contains(id, "-v") {
				t.Fatalf("live change event %s arrived, want none for hide and clear", id)
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

func TestRealInfraHideAndClearApplyOnlyToTheReader(t *testing.T) {
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
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 1 {
		t.Fatalf("ClearHistory(1) = %v, %v; want cleared before seq 1", resp, err)
	}
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || !hiddenOnly(got[1]) || got[2].GetHidden() || got[2].GetText() != "visible 2" || !hiddenOnly(got[3]) {
		t.Fatalf("bob's history = %v, want seq 1 cleared, seq 2 visible, seq 3 hidden, every seq kept", got)
	}
	for seq, m := range historyAs(t, client, itUser, roomID) {
		if m.GetHidden() || m.GetText() != "visible "+strconv.FormatUint(seq, 10) {
			t.Fatalf("alice sees seq %d as %v, want it visible", seq, m)
		}
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(latest) = %v, %v; want cleared before seq 3", resp, err)
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(1) after 3 = %v, %v; want it to stay at 3", resp, err)
	}
	for seq, m := range historyAs(t, client, "bob", roomID) {
		if !hiddenOnly(m) {
			t.Fatalf("bob sees seq %d as %v after clearing everything, want a hidden placeholder", seq, m)
		}
	}
	assertNoChangeEvents(t, live, 3*time.Second)
}
