package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestMessageChangedPicksTheEventByFactKind(t *testing.T) {
	m := sample()
	edit := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "sửa", At: sentAt}
	if got, want := pbconv.MessageChanged(domain.RoomGroup, m, edit), pbconv.MessageEdited(domain.RoomGroup, m, edit); !proto.Equal(got, want) {
		t.Fatalf("edit fact: got %v, want %v", got, want)
	}
	del := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: domain.EditDelete, Tenant: "acme", By: "alice", At: sentAt}
	if got, want := pbconv.MessageChanged(domain.RoomDM, m, del), pbconv.MessageDeleted(domain.RoomDM, m, del); !proto.Equal(got, want) {
		t.Fatalf("delete fact: got %v, want %v", got, want)
	}
}
