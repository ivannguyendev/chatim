package itest

import (
	"bytes"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/app"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/resync"
)

const itResyncDuplicates = 3 * time.Second

func TestRealInfraResyncRepublishesTheCurrentStateAfterTheEventStreamLostIt(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastSweep)
	env["EVT_STREAM_DUPLICATES"] = itResyncDuplicates.String()
	chat := startChatNode(t, it, env)
	client := dialChat(t, chat.cfg)
	began := time.Now().UTC()
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	st := itStore(it, chat)
	parent, reply, mention := writeEveryKind(t, it, chat.cfg, client, roomID)
	if reply != parent+1 {
		t.Fatalf("reply seq = %d, want %d", reply, parent+1)
	}
	chat.awaitSweeper(t)
	awaitSettled(t, it, chat.cfg, st, room)
	before := readLinkProjections(t, it, chat.cfg, st, room, parent, mention)
	want := currentStateIDs(t, st, room, parent, mention)
	time.Sleep(2 * itResyncDuplicates)

	stream, err := it.js.Stream(t.Context(), chat.cfg.Stream.Name)
	if err != nil {
		t.Fatalf("event stream: %v", err)
	}
	if err := stream.Purge(t.Context()); err != nil {
		t.Fatalf("purge the event stream: %v", err)
	}
	if left := it.streamIDs(t, chat.cfg.Stream.Name); len(left) != 0 {
		t.Fatalf("event stream after purge holds %v, want nothing", left)
	}
	live := subscribeLive(t, it, chat.cfg, roomID)

	opts := resync.Options{From: began.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute), Tenant: itTenant, Rate: 1000}
	var out bytes.Buffer
	ran := time.Now()
	if err := app.RunResync(t.Context(), chat.cfg, opts, quiet, &out); err != nil {
		t.Fatalf("RunResync: %v (output %q)", err, out.String())
	}
	line := "resync rooms=1 marked rooms.created=1 rooms.member_count=1 rooms.info=0 messages=3 message_edits=1 message_interactions.reaction=1 message_interactions.bookmark=0 pin_actions=1 members=3 members.read=3 members.cleared=1 members.preferences=0 hidden=1 count_checks=2 dry_run=false"
	if got := strings.TrimSpace(out.String()); got != line {
		t.Fatalf("resync output = %q,\nwant %q", got, line)
	}
	awaitLiveIDs(t, live, ran, want...)
	stored := it.streamIDs(t, chat.cfg.Stream.Name)
	for _, id := range want {
		if stored[id] != 1 {
			t.Errorf("event %s stored %d times after resync, want once", id, stored[id])
		}
	}
	awaitSettled(t, it, chat.cfg, st, room)
	assertLinksUnchanged(t, before, readLinkProjections(t, it, chat.cfg, st, room, parent, mention))
}
