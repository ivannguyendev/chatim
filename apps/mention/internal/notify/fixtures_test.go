package notify_test

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/plan"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	sentAt   = time.UnixMilli(1_700_000_000_000).UTC()
	editedAt = sentAt.Add(7 * time.Minute)
)

func result(kind plan.Kind, ver uint32) plan.Result {
	return plan.Result{
		Plan:     domain.Plan{Key: domain.MessageKey{Room: 777, Thread: 0, Seq: 41}, Tenant: "t1", Sender: "Lan", Ver: ver},
		RoomType: domain.RoomGroup, Sender: "Lan", Ts: editedAt, Kind: kind,
	}
}

func snapshot() *chatimv1.Message {
	return &chatimv1.Message{RoomId: "777", Seq: 41, Sender: "Lan", Text: "hi @Minh", Ver: 1}
}
