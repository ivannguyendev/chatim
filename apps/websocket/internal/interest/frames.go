package interest

import (
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var readyFrame = mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}})

var resyncFrames = map[chatimv1.ResyncReason][]byte{
	chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD:    resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD),
	chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST:  resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST),
	chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG: resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG),
}

func resyncFrame(reason chatimv1.ResyncReason) []byte {
	return mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Resync{Resync: &chatimv1.ResyncFrame{Reason: reason}}})
}

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
