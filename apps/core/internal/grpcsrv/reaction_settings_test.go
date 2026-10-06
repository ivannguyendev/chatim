package grpcsrv_test

import (
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReactionSettingsListTheConfiguredEmojis(t *testing.T) {
	defaults := newRig(t, options{})
	got, err := defaults.client.GetReactionSettings(as(t, "acme", "alice"), &chatimv1.GetReactionSettingsRequest{})
	if err != nil || !slices.Equal(got.GetEmojis(), []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}) {
		t.Fatalf("GetReactionSettings = %v, %v; want the default list in order", got, err)
	}
	own := newRig(t, options{limits: mutate.Limits{Emojis: []string{"🎉", "👍"}}})
	got, err = own.client.GetReactionSettings(as(t, "other", "bob"), &chatimv1.GetReactionSettingsRequest{})
	if err != nil || !slices.Equal(got.GetEmojis(), []string{"🎉", "👍"}) {
		t.Fatalf("GetReactionSettings = %v, %v; want the configured list", got, err)
	}
	_, err = own.client.GetReactionSettings(t.Context(), &chatimv1.GetReactionSettingsRequest{})
	expectCode(t, err, codes.Unauthenticated)
}

func TestReactWithAnUnlistedEmojiIsInvalid(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "hi")
	_, err := rg.client.ReactMessage(as(t, "acme", "bob"), &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "🎉"})
	expectCode(t, err, codes.InvalidArgument)
}
