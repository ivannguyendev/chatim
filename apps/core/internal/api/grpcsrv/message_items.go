package grpcsrv

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func messagesOf(items []message.Item) []*chatimv1.Message {
	out := make([]*chatimv1.Message, len(items))
	for i, it := range items {
		out[i] = pbconv.Message(it.Message)
		if it.ReplyPreview != nil {
			out[i].ReplyPreview = pbconv.ReplyPreview(*it.ReplyPreview)
		}
		out[i].MyReaction, out[i].Bookmarked = it.MyReaction, it.Bookmarked
	}
	return out
}
