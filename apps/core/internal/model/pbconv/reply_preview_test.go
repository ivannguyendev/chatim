package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReplyPreviewShowsTextOnlyWhenTheReaderMaySeeIt(t *testing.T) {
	parent := domain.Message{Room: 7, Seq: 40, From: "lan", Text: "parent text"}
	deleted, hidden := parent, parent
	deleted.Deleted = true
	hidden.Hidden = true
	for name, c := range map[string]struct {
		in   domain.Message
		want *chatimv1.ReplyPreview
	}{
		"visible": {parent, &chatimv1.ReplyPreview{Seq: 40, Sender: "lan", Text: "parent text"}},
		"deleted": {deleted, &chatimv1.ReplyPreview{Seq: 40, Sender: "lan", Deleted: true}},
		"hidden":  {hidden, &chatimv1.ReplyPreview{Seq: 40, Sender: "lan", Hidden: true}},
	} {
		if got := pbconv.ReplyPreview(c.in); !proto.Equal(got, c.want) {
			t.Errorf("%s: ReplyPreview = %v, want %v", name, got, c.want)
		}
	}
}
