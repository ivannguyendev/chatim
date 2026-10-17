package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestMentionOfFindsTheItemAndItsReadFlag(t *testing.T) {
	items := []*chatimv1.MentionItem{{Message: &chatimv1.Message{RoomId: noticeRoom, Seq: 5}}, {Message: &chatimv1.Message{RoomId: noticeRoom, Seq: 3}, Read: true}}
	if found, read := e2e.MentionOf(items, e2e.Place{Room: noticeRoom, Seq: 5}); !found || read {
		t.Fatalf("MentionOf seq 5 = %v, %v; want found unread", found, read)
	}
	if found, read := e2e.MentionOf(items, e2e.Place{Room: noticeRoom, Seq: 3}); !found || !read {
		t.Fatalf("MentionOf seq 3 = %v, %v; want found read", found, read)
	}
	if found, _ := e2e.MentionOf(items, e2e.Place{Room: "78", Seq: 5}); found {
		t.Fatal("MentionOf found seq 5 of another room")
	}
}

func TestCheckChangedComparesSets(t *testing.T) {
	if err := e2e.CheckChanged("set", []string{"b", "a"}, "a", "b"); err != nil {
		t.Fatalf("CheckChanged in another order = %v", err)
	}
	if err := e2e.CheckChanged("set", nil); err != nil {
		t.Fatalf("CheckChanged of nothing = %v", err)
	}
	if err := e2e.CheckChanged("set", []string{"a"}); err == nil {
		t.Fatal("CheckChanged with an extra user = nil, want an error")
	}
}

func TestFormatRoomGroupsSortsByID(t *testing.T) {
	got := e2e.FormatRoomGroups([]*chatimv1.RoomGroup{{Id: "team-sale", Name: "Team Sale"}, {Id: "hr", Name: "HR"}})
	if got != "hr=HR,team-sale=Team Sale" {
		t.Fatalf("FormatRoomGroups = %q", got)
	}
}
