package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/notify"
	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

func notice(t *testing.T, user string) notify.Notice {
	ns, err := builder.Notified(result(plan.Edited, 1), snapshot(), map[string]domain.Target{user: {Kind: domain.TargetUser, ID: user}})
	noErr(t, err)
	return ns[0]
}

func retraction(t *testing.T, users ...string) notify.Notice {
	drop := map[string]domain.Target{}
	for _, u := range users {
		drop[u] = domain.Target{Kind: domain.TargetUser, ID: u}
	}
	ns, err := builder.Retracted(result(plan.Edited, 1), drop)
	noErr(t, err)
	return ns[0]
}

func TestBatchPublishesWithMsgIDAndWaitsForAcks(t *testing.T) {
	js, stats := &fakeJetStream{}, &notify.Stats{}
	b := notify.NewPublisher(js, stats).Batch()
	noErr(t, b.Add(notice(t, "Minh")))
	noErr(t, b.Add(retraction(t, "Lan", "Hoa")))
	noErr(t, b.Wait(context.Background()))
	equal(t, len(js.sent), 2)
	equal(t, "websocket.publish.t1", js.sent[0].Subject)
	equal(t, notice(t, "Minh").Packet.GetId(), js.sent[0].Header.Get(jetstream.MsgIDHeader))
	equal(t, wspublish.PageID("777-0-41-r1-edited", 1, []string{"Hoa", "Lan"}), js.sent[1].Header.Get(jetstream.MsgIDHeader))
	got := &chatimv1.WsPublish{}
	noErr(t, proto.Unmarshal(js.sent[0].Data, got))
	truth(t, proto.Equal(notice(t, "Minh").Packet, got))
	equal(t, uint64(1), stats.Notified())
	equal(t, uint64(2), stats.Retracted())
	equal(t, stats.Failures(), uint64(0))
}

func TestBatchDuplicateAckIsNotCounted(t *testing.T) {
	js, stats := &fakeJetStream{}, &notify.Stats{}
	p := notify.NewPublisher(js, stats)
	for range 2 {
		b := p.Batch()
		noErr(t, b.Add(notice(t, "Minh")))
		noErr(t, b.Wait(context.Background()))
	}
	equal(t, uint64(1), stats.Notified())
}

func TestBatchWaitReportsOneFailedAck(t *testing.T) {
	lan := notice(t, "Lan").Packet.GetId()
	js := &fakeJetStream{failAck: map[string]bool{lan: true}}
	stats := &notify.Stats{}
	b := notify.NewPublisher(js, stats).Batch()
	noErr(t, b.Add(notice(t, "Minh")))
	noErr(t, b.Add(notice(t, "Lan")))
	noErr(t, b.Add(notice(t, "Hoa")))
	err := b.Wait(context.Background())
	errIs(t, err, errAck)
	errContains(t, err, lan)
	equal(t, uint64(2), stats.Notified())
	equal(t, uint64(1), stats.Failures())
}

func TestBatchAddRefusedIsReportedByWait(t *testing.T) {
	boom := errors.New("too many pending")
	stats := &notify.Stats{}
	b := notify.NewPublisher(&fakeJetStream{refuse: boom}, stats).Batch()
	errIs(t, b.Add(notice(t, "Minh")), boom)
	errIs(t, b.Wait(context.Background()), boom)
	equal(t, uint64(1), stats.Failures())
}

func TestBatchAddRejectsEmptyNotice(t *testing.T) {
	b := notify.NewPublisher(&fakeJetStream{}, &notify.Stats{}).Batch()
	errIs(t, b.Add(notify.Notice{}), apperr.ErrInvalidArgument)
}

func TestBatchWaitEndsWithContext(t *testing.T) {
	js, stats := &fakeJetStream{hold: true}, &notify.Stats{}
	b := notify.NewPublisher(js, stats).Batch()
	noErr(t, b.Add(notice(t, "Minh")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errIs(t, b.Wait(ctx), context.Canceled)
	equal(t, stats.Notified(), uint64(0))
}

func TestEmptyBatchWaitIsNil(t *testing.T) {
	noErr(t, notify.NewPublisher(&fakeJetStream{}, &notify.Stats{}).Batch().Wait(context.Background()))
}
