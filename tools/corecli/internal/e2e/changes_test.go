package e2e_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func changedPage(want []e2e.Ack) []*chatimv1.Message {
	got := messagesOf(want)
	got[0].Text, got[0].Version = e2e.EditTextFor(1), 1
	got[1].Text, got[1].Version, got[1].Deleted = "", 1, true
	return got
}

func TestCheckPageExpectsTheEditedAndDeletedContent(t *testing.T) {
	want := acks(3)
	changes := []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}
	if err := e2e.CheckPage(want, changes, changedPage(want), room, sender); err != nil {
		t.Fatalf("CheckPage(changed) = %v", err)
	}
	expectErr(t, e2e.CheckPage(want, nil, changedPage(want), room, sender), "text")
	expectErr(t, e2e.CheckPage(want, changes, messagesOf(want), room, sender), "text")
	undeleted := changedPage(want)
	undeleted[1].Deleted = false
	expectErr(t, e2e.CheckPage(want, changes, undeleted, room, sender), "deleted")
	hidden := changedPage(want)
	hidden[2].Hidden = true
	expectErr(t, e2e.CheckPage(want, changes, hidden, room, sender), "hidden")
}

func TestCheckChangeEventsFindsEachChangeByID(t *testing.T) {
	changes := []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}
	edited := e2e.Event{Kind: e2e.KindEdited, Room: room, ID: e2e.ChangeEventID(room, 1, 1), Seq: 1, Version: 1, Text: e2e.EditTextFor(1)}
	deleted := e2e.Event{Kind: e2e.KindDeleted, Room: room, ID: e2e.ChangeEventID(room, 2, 1), Seq: 2, Version: 1}
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1, CID: "x"}
	if got := e2e.ChangeEventID("42", 1, 1); got != "42-0-1-v1" {
		t.Fatalf("ChangeEventID = %q, want 42-0-1-v1", got)
	}
	missing, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{created, edited})
	if err != nil || len(missing) != 1 || missing[0] != deleted.ID {
		t.Fatalf("CheckChangeEvents = %v, %v; want only %s missing", missing, err, deleted.ID)
	}
	if missing, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{edited, deleted, edited}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckChangeEvents(all, one duplicate) = %v, %v; want none missing", missing, err)
	}
	bad := map[string]e2e.Event{
		"unexpected":    {Kind: e2e.KindEdited, Room: room, ID: e2e.ChangeEventID(room, 3, 1), Seq: 3, Version: 1},
		"is msg_edited": {Kind: e2e.KindEdited, Room: room, ID: deleted.ID, Seq: 2, Version: 1},
		"carries":       {Kind: e2e.KindEdited, Room: room, ID: edited.ID, Seq: 9, Version: 1, Text: edited.Text},
		"text":          {Kind: e2e.KindEdited, Room: room, ID: edited.ID, Seq: 1, Version: 1, Text: "other"},
	}
	for part, ev := range bad {
		_, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{ev})
		expectErr(t, err, part)
	}
}

func TestCheckVersionsWantsTheOriginalThenTheEdit(t *testing.T) {
	as := acks(2)
	at := timestamppb.Now()
	good := []*chatimv1.MessageVersion{
		{Version: 0, Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: e2e.TextFor(as[0].CID), By: sender, At: at},
		{Version: 1, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: e2e.EditTextFor(1), By: sender, At: at},
	}
	if err := e2e.CheckVersions(as, e2e.EditOf(1), good, sender); err != nil {
		t.Fatalf("CheckVersions(edit) = %v", err)
	}
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(1), good[:1], sender), "1 versions")
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(1), good, "bob"), "by")
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(5), good, sender), "never acked")
	if err := e2e.CheckVersions(as, e2e.DeleteOf(2), nil, sender); err != nil {
		t.Fatalf("CheckVersions(delete, none) = %v", err)
	}
	expectErr(t, e2e.CheckVersions(as, e2e.DeleteOf(2), good, sender), "want none")
}
