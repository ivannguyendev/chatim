package e2e_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestStateRoundTripsThroughAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if _, err := e2e.Load(path); err == nil {
		t.Fatal("Load of a missing state succeeded")
	}
	want := e2e.State{Tenant: "e2e", User: "alice", Room: room, Owner: "core-1", Acks: acks(3)}
	if err := e2e.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := e2e.Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("state dir holds %d entries, want only the state file", len(entries))
	}
}

func TestReadEventsSkipsAnUnfinishedLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if evs, err := e2e.ReadEvents(path); err != nil || len(evs) != 0 {
		t.Fatalf("ReadEvents of a missing file = %v, %v; want none yet", evs, err)
	}
	content := `{"room":"42","id":"42-0-1","seq":1,"cid":"a"}` + "\n" + `{"room":"42","id":"42-0-2","seq":2,"cid":"b"}` + "\n" + `{"room":"42","id":"42-0-3"`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	evs, err := e2e.ReadEvents(path)
	if err != nil || len(evs) != 2 || evs[1] != (e2e.Event{Room: "42", ID: "42-0-2", Seq: 2, CID: "b"}) {
		t.Fatalf("ReadEvents = %+v, %v", evs, err)
	}
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e2e.ReadEvents(path); err == nil {
		t.Fatal("ReadEvents accepted a malformed line")
	}
}

func TestEventOfReadsTheCreatedMessage(t *testing.T) {
	ev := &chatimv1.Event{RoomId: room, Id: "42-0-4", Seq: 4, Payload: &chatimv1.Event_MessageCreated{
		MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{Cid: "a-4"}},
	}}
	got, ok := e2e.EventOf("live.e2e.room.42.evt.msg_created", ev)
	if !ok {
		t.Fatal("EventOf skipped a message_created event")
	}
	want := e2e.Event{Room: room, ID: "42-0-4", Seq: 4, CID: "a-4", Subject: "live.e2e.room.42.evt.msg_created"}
	if got != want {
		t.Fatalf("EventOf = %+v, want %+v", got, want)
	}
}
