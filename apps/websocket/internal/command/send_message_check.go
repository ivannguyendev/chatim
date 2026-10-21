package command

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errInternalSendField = status.Error(codes.InvalidArgument, "kind, skip_unread and meta are not allowed")

func checkSendMessage(m proto.Message) error {
	req, ok := m.(*chatimv1.SendMessageRequest)
	if !ok || req.GetKind() != chatimv1.MessageKind_MESSAGE_KIND_UNSPECIFIED || req.GetSkipUnread() || len(req.GetMeta()) > 0 {
		return errInternalSendField
	}
	return nil
}
