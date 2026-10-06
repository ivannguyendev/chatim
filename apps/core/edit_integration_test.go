package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func callerAs(ctx context.Context, user string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, grpcsrv.TenantHeader, itTenant, grpcsrv.UserHeader, user)
}

func sendAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID, cid, text string) uint64 {
	t.Helper()
	resp, err := client.SendMessage(callerAs(t.Context(), user), &chatimv1.SendMessageRequest{RoomId: roomID, Cid: cid, Text: text})
	if err != nil {
		t.Fatalf("SendMessage(%s as %s): %v", cid, user, err)
	}
	return resp.GetSeq()
}

func historyAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID string) map[uint64]*chatimv1.Message {
	t.Helper()
	req := &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 50}
	resp, err := client.GetHistory(callerAs(t.Context(), user), req)
	if err != nil {
		t.Fatalf("GetHistory as %s: %v", user, err)
	}
	out := make(map[uint64]*chatimv1.Message, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		out[m.GetSeq()] = m
	}
	return out
}

func editHistory(t *testing.T, client chatimv1.CoreServiceClient, roomID string, seq uint64) []*chatimv1.MessageVersion {
	t.Helper()
	resp, err := client.GetEditHistory(caller(t.Context()), &chatimv1.GetEditHistoryRequest{RoomId: roomID, Seq: seq, Limit: 100})
	if err != nil {
		t.Fatalf("GetEditHistory(%d): %v", seq, err)
	}
	return resp.GetVersions()
}

func awaitLiveEvent(t *testing.T, live <-chan *nats.Msg, id string) *chatimv1.Event {
	t.Helper()
	deadline := time.After(itLiveLimit)
	for {
		select {
		case m := <-live:
			if m.Header.Get(jetstream.MsgIDHeader) != id {
				continue
			}
			ev := &chatimv1.Event{}
			if err := proto.Unmarshal(m.Data, ev); err != nil {
				t.Fatalf("decode live event %s: %v", id, err)
			}
			return ev
		case <-deadline:
			t.Fatalf("live event %s did not arrive within %v", id, itLiveLimit)
		}
	}
}

func TestRealInfraEditShowsInHistoryEditHistoryAndLive(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-a", "before")

	req := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "after"}
	for attempt := range 2 {
		resp, err := client.EditMessage(caller(t.Context()), req)
		if m := resp.GetMessage(); err != nil || m.GetVersion() != 1 || m.GetText() != "after" || m.GetDeleted() || m.GetEditedAt() == nil {
			t.Fatalf("EditMessage attempt %d = %v, %v; want version 1 with the new text", attempt+1, m, err)
		}
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 1))
	if e := ev.GetMessageEdited(); e.GetVersion() != 1 || e.GetMessage().GetText() != "after" || ev.GetActor() != itUser {
		t.Fatalf("msg_edited event = %v, want version 1 with the new text by %s", ev, itUser)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; m.GetText() != "after" || m.GetVersion() != 1 || m.GetDeleted() || m.GetHidden() {
		t.Fatalf("history shows %v, want the edited text at version 1", m)
	}
	v := editHistory(t, client, roomID, seq)
	if len(v) != 2 || v[0].GetKind() != chatimv1.EditKind_EDIT_KIND_ORIGINAL || v[0].GetText() != "before" ||
		v[1].GetKind() != chatimv1.EditKind_EDIT_KIND_TEXT || v[1].GetText() != "after" || v[1].GetVersion() != 1 || v[1].GetBy() != itUser {
		t.Fatalf("edit history = %v, want the original then version 1", v)
	}
	missing := &chatimv1.GetEditHistoryRequest{RoomId: roomID, Seq: seq + 1000, Limit: 100}
	if _, err := client.GetEditHistory(caller(t.Context()), missing); status.Code(err) != codes.NotFound {
		t.Fatalf("GetEditHistory of a missing message = %v, want NotFound", err)
	}
}

func TestRealInfraWorkersProjectAnEditWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-b", "original")
	core.awaitTerm(t)

	st := itStore(it, core)
	fact := domain.Edit{
		Room: room, Seq: seq, Version: 1, Kind: domain.EditText, Tenant: itTenant, By: itUser,
		Text: "edited outside the core", Prev: "original", At: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := st.Append(t.Context(), fact); err != nil {
		t.Fatalf("append an edit outside the core: %v", err)
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 1))
	if m := ev.GetMessageEdited().GetMessage(); m.GetText() != fact.Text || m.GetVersion() != 1 {
		t.Fatalf("msg_edited from the workers = %v, want the projected snapshot at version 1", ev)
	}
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(got) != 1 || got[0].Text != fact.Text || got[0].Version != 1 || !got[0].EditedAt.Equal(fact.At) {
		t.Fatalf("projection = %+v, %v; want the outside edit at version 1", got, err)
	}
}

func TestRealInfraDeletePurgesOlderTextsAndBlocksLaterEdits(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-c", "secret v0")
	if _, err := client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "secret v1"}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}

	del := &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq, BaseVersion: 1}
	for attempt := range 2 {
		resp, err := client.DeleteMessage(caller(t.Context()), del)
		if m := resp.GetMessage(); err != nil || !m.GetDeleted() || m.GetText() != "" || m.GetVersion() != 2 {
			t.Fatalf("DeleteMessage attempt %d = %v, %v; want deleted at version 2 without text", attempt+1, m, err)
		}
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 2))
	if d := ev.GetMessageDeleted(); d.GetVersion() != 2 || !d.GetMessage().GetDeleted() || d.GetMessage().GetText() != "" {
		t.Fatalf("msg_deleted event = %v, want version 2 without text", ev)
	}
	facts, err := itStore(it, core).History(t.Context(), store.MsgKey{Room: room, Seq: seq}, 0, store.MaxEditPage)
	if err != nil || len(facts) != 2 || facts[0].Text != "" || facts[0].Prev != "" || facts[1].Kind != domain.EditDelete {
		t.Fatalf("facts after delete = %+v, %v; want v1 without text or prev, then the delete", facts, err)
	}
	if v := editHistory(t, client, roomID, seq); len(v) != 0 {
		t.Fatalf("edit history of a deleted message = %v, want none", v)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; !m.GetDeleted() || m.GetText() != "" || m.GetVersion() != 2 {
		t.Fatalf("history shows %v, want a deleted placeholder at version 2", m)
	}
	late := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, BaseVersion: 2, Text: "too late"}
	if _, err := client.EditMessage(caller(t.Context()), late); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("EditMessage after delete = %v, want FailedPrecondition", err)
	}
}
