package grpcsrv_test

import (
	"context"
	"strconv"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type shown struct {
	seq             uint64
	text            string
	deleted, hidden bool
	version         uint32
}

func TestHistoryShowsPlaceholdersPerViewer(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		rg.send(t, alice, room, "c-"+strconv.Itoa(i), text)
	}
	steps := []func() error{
		func() error {
			_, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2})
			return err
		},
		func() error {
			_, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 4, Text: "four!"})
			return err
		},
		func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3})
			return err
		},
		func() error {
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 1})
			return err
		},
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	cases := []struct {
		name string
		ctx  context.Context
		want []shown
	}{
		{"bob", bob, []shown{{1, "", false, true, 0}, {2, "", true, false, 1}, {3, "", false, true, 0}, {4, "four!", false, false, 1}}},
		{"alice", alice, []shown{{1, "one", false, false, 0}, {2, "", true, false, 1}, {3, "three", false, false, 0}, {4, "four!", false, false, 1}}},
	}
	for _, c := range cases {
		resp, err := rg.client.GetHistory(c.ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
		if err != nil {
			t.Fatalf("%s GetHistory: %v", c.name, err)
		}
		msgs := resp.GetMessages()
		if len(msgs) != len(c.want) {
			t.Fatalf("%s got %d messages, want %d", c.name, len(msgs), len(c.want))
		}
		for i, m := range msgs {
			if got := (shown{m.GetSeq(), m.GetText(), m.GetDeleted(), m.GetHidden(), m.GetVersion()}); got != c.want[i] {
				t.Fatalf("%s message %d = %+v, want %+v", c.name, i, got, c.want[i])
			}
		}
	}
}
