package app

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestMediaChatNodeMessageFilesReadsAnyMessageWithoutAViewer(t *testing.T) {
	f := newMediaFixture(t)
	f.insert(t, privateRoom, 0, 1, false, photo("one.png"), domain.Attachment{Key: mediaFileKey + "x", Name: "r.pdf", Mime: "application/pdf", Size: 3})
	f.insert(t, mediaRoom, 1, 2, false, photo("reply.png"))
	f.insert(t, mediaRoom, 0, 3, false)
	f.insert(t, mediaRoom, 0, 4, true, photo("deleted.png"))
	for _, tc := range []struct {
		name string
		ref  mediaapp.MessageRef
		want mediaapp.MessageState
	}{
		{"live with files", ref("88", 0, 1), mediaapp.MessageState{Found: true, Tenant: mediaTenant, Keys: []string{mediaFileKey, mediaFileKey + "x"}}},
		{"thread reply", ref("77", 1, 2), mediaapp.MessageState{Found: true, Tenant: mediaTenant, Keys: []string{mediaFileKey}}},
		{"live without files", ref("77", 0, 3), mediaapp.MessageState{Found: true, Tenant: mediaTenant}},
		{"deleted", ref("77", 0, 4), mediaapp.MessageState{Found: true, Deleted: true, Tenant: mediaTenant}},
		{"missing", ref("77", 0, 9), mediaapp.MessageState{}},
		{"missing thread", ref("77", 5, 2), mediaapp.MessageState{}},
	} {
		got, err := f.node.MessageFiles(t.Context(), tc.ref)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.Found != tc.want.Found || got.Deleted != tc.want.Deleted || got.Tenant != tc.want.Tenant || !slices.Equal(got.Keys, tc.want.Keys) {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
	got, err := f.node.MessageFiles(t.Context(), ref("88", 0, 1))
	want := map[string]string{mediaFileKey: "image/png", mediaFileKey + "x": "application/pdf"}
	if err != nil || !got.SentAt.Equal(mediaSent.Add(time.Second)) || !maps.Equal(got.Mimes, want) {
		t.Fatalf("live message sent at %v mimes %v, %v; want %v and %v", got.SentAt, got.Mimes, err, mediaSent.Add(time.Second), want)
	}
}

func TestMediaChatNodeMessageFilesRejectsBadRefs(t *testing.T) {
	f := newMediaFixture(t)
	for _, bad := range []mediaapp.MessageRef{ref("", 0, 1), ref("abc", 0, 1), ref("0", 0, 1), ref("-7", 0, 1), ref("77", 0, 0)} {
		if _, err := f.node.MessageFiles(t.Context(), bad); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}
