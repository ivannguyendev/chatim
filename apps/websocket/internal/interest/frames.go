package interest

import (
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var readyFrame = mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}})

func roomClosedFrame(room string, reason chatimv1.RoomClosedReason) []byte {
	return mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_RoomClosed{
		RoomClosed: &chatimv1.RoomClosedFrame{RoomId: room, Reason: reason},
	}})
}

func mustMarshal(m proto.Message) []byte {
	out, err := proto.Marshal(m)
	if err != nil {
		panic("interest: encode server frame: " + err.Error())
	}
	return out
}
