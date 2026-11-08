package pbconv_test

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestAttachmentsRoundTrip(t *testing.T) {
	list := []domain.Attachment{
		{Key: "acme/lan/20261107/a", Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}, Hash: "sha256:ab"},
		{Key: "acme/lan/20261107/b", Name: "clip.mp4", Mime: "video/mp4", Size: 1, Dur: 12000},
	}
	want := []*chatimv1.Attachment{
		{Key: "acme/lan/20261107/a", Name: "ảnh.png", Mime: "image/png", Size: 2048, Dim: []int32{640, 480}, Hash: "sha256:ab"},
		{Key: "acme/lan/20261107/b", Name: "clip.mp4", Mime: "video/mp4", Size: 1, Dur: 12000},
	}
	got := pbconv.Attachments(list)
	if len(got) != len(want) {
		t.Fatalf("Attachments = %v, want %v", got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("Attachments[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	back := pbconv.DomainAttachments(got)
	if !reflect.DeepEqual(back, list) {
		t.Fatalf("DomainAttachments = %+v, want %+v", back, list)
	}
	got[0].Dim[0] = 1
	if list[0].Dim[0] != 640 || back[0].Dim[0] != 640 {
		t.Fatalf("conversions share dim slices: %v %v", list[0].Dim, back[0].Dim)
	}
}

func TestAttachmentsEmptyIsNil(t *testing.T) {
	if got := pbconv.Attachments([]domain.Attachment{}); got != nil {
		t.Fatalf("Attachments(empty) = %v, want nil", got)
	}
	if got := pbconv.DomainAttachments([]*chatimv1.Attachment{}); got != nil {
		t.Fatalf("DomainAttachments(empty) = %v, want nil", got)
	}
	got := pbconv.DomainAttachments([]*chatimv1.Attachment{{Key: "k", Dim: []int32{}}})
	if got[0].Dim != nil {
		t.Fatalf("empty dim = %#v, want nil", got[0].Dim)
	}
}
