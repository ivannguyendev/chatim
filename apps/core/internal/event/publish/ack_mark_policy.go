package publish

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MarkDeadline(ackTimeout time.Duration) time.Duration {
	return ackTimeout + markWindow + markTimeout
}

func markKey(room uint64, ev *chatimv1.Event) (store.MsgKey, bool) {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return store.MsgKey{Room: room, Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()}, true
	default:
		return store.MsgKey{}, false
	}
}
