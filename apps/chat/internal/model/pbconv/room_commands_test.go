package pbconv_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestDomainMessageKind(t *testing.T) {
	cases := []struct {
		in   chatimv1.MessageKind
		want domain.Kind
		ok   bool
	}{
		{chatimv1.MessageKind_MESSAGE_KIND_UNSPECIFIED, domain.KindText, true},
		{chatimv1.MessageKind_MESSAGE_KIND_TEXT, domain.KindText, true},
		{chatimv1.MessageKind_MESSAGE_KIND_SYSTEM, domain.KindSystem, true},
		{chatimv1.MessageKind(3), 0, false},
		{chatimv1.MessageKind(-1), 0, false},
	}
	for _, c := range cases {
		got, err := pbconv.DomainMessageKind(c.in)
		if got != c.want || (err == nil) != c.ok || (!c.ok && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("DomainMessageKind(%v) = %d, %v; want %d, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestDomainPreferencesChangeKeepsPresence(t *testing.T) {
	if got := pbconv.DomainPreferencesChange(&chatimv1.SetRoomPreferencesRequest{RoomId: "777"}); got != (domain.PreferencesChange{}) {
		t.Fatalf("no fields = %+v, want an empty change", got)
	}
	until := sentAt.Add(time.Hour)
	got := pbconv.DomainPreferencesChange(&chatimv1.SetRoomPreferencesRequest{
		RoomId: "777", Pinned: new(false), MutedUntil: timestamppb.New(until), Archived: new(true),
	})
	if got.Pinned == nil || *got.Pinned || got.Archived == nil || !*got.Archived || got.MutedUntil == nil || !got.MutedUntil.Equal(until) {
		t.Fatalf("all fields = %+v, want pinned false, archived true, muted until %v", got, until)
	}
}

func TestDomainRoomUpdateKeepsPresence(t *testing.T) {
	if got := pbconv.DomainRoomUpdate(&chatimv1.UpdateRoomRequest{RoomId: "777"}); got != (domain.RoomUpdate{}) {
		t.Fatalf("no fields = %+v, want an empty update", got)
	}
	got := pbconv.DomainRoomUpdate(&chatimv1.UpdateRoomRequest{RoomId: "777", Name: new("Kho"), Avatars: &chatimv1.RoomAvatars{}})
	if got.Name == nil || *got.Name != "Kho" || got.Avatars == nil || len(*got.Avatars) != 0 {
		t.Fatalf("both fields = %+v, want name Kho and no avatars", got)
	}
	keys := []string{"acme/minh/pub/20261113/0cr1"}
	got = pbconv.DomainRoomUpdate(&chatimv1.UpdateRoomRequest{RoomId: "777", Avatars: &chatimv1.RoomAvatars{Keys: keys}})
	if got.Name != nil || got.Avatars == nil || len(*got.Avatars) != 1 || (*got.Avatars)[0] != keys[0] {
		t.Fatalf("avatars = %+v, want the one key", got)
	}
}
