package interest

import (
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var readyFrame = mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Ready{Ready: &chatimv1.ReadyFrame{}}})

var resyncFrames = map[chatimv1.ResyncReason][]byte{
	chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD:    resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_LAG_TOO_OLD),
	chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST:  resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_CONSUMER_LOST),
	chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG: resyncFrame(chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG),
}

const loadRetryHint = 3 * time.Second

func loadFailedFrame() []byte {
	var hint uint32
	if ms := backoff.Jitter(loadRetryHint).Milliseconds(); ms > 0 && ms <= math.MaxUint32 {
		hint = uint32(ms)
	}
	return mustMarshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_LoadFailed{
		LoadFailed: &chatimv1.LoadFailedFrame{RetryAfterMs: hint},
	}})
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
