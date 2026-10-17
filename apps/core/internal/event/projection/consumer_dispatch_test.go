package projection_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func everyKind(t *testing.T) []*fakeDelivery {
	t.Helper()
	ds := make([]*fakeDelivery, 0, len(kindCases)+1)
	for i, c := range kindCases {
		ev := envelope(roomA+uint64(i%3), 0, uint64(i+1), at(i))
		c.set(ev)
		ds = append(ds, delivery(t, ev, 1))
		if i == 5 {
			ds = append(ds, &fakeDelivery{data: []byte("junk")})
		}
	}
	return ds
}

func seqsOf(ds []*fakeDelivery, keep func(*chatimv1.Event) bool) []uint64 {
	var out []uint64
	for _, d := range ds {
		ev := &chatimv1.Event{}
		if err := proto.Unmarshal(d.data, ev); err != nil || !keep(ev) {
			continue
		}
		out = append(out, ev.GetSeq())
	}
	return out
}

func TestConsumerGivesEachHandlerTheKindsItKeepsInOneBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		index := newFakeHandler(projection.MentionReplyIndexName, newIndexRig(t).index.Keeps)
		reads := newFakeHandler(projection.MentionReadName, newReadRig(t).read.Keeps)
		rg := newConsumerRig(t, t.Context(), index, reads)
		ds := everyKind(t)
		rg.src.push(ds...)
		synctest.Wait()
		touched := rg.writer.touched()
		if len(touched) != 1 || len(touched[0]) != 3 {
			t.Fatalf("touched %v, want one write of the three rooms", touched)
		}
		listed := 0
		for _, c := range kindCases {
			if c.listed {
				listed++
			}
		}
		if listed != 13 {
			t.Fatalf("%d kinds touch activity, want 13", listed)
		}
		wantIndex := seqsOf(ds, func(ev *chatimv1.Event) bool {
			switch ev.GetPayload().(type) {
			case *chatimv1.Event_MessageCreated, *chatimv1.Event_MessageEdited, *chatimv1.Event_MessageDeleted:
				return true
			}
			return false
		})
		wantReads := seqsOf(ds, func(ev *chatimv1.Event) bool { _, ok := ev.GetPayload().(*chatimv1.Event_ReadUpdated); return ok })
		if got := index.handled(); len(got) != 1 || !slices.Equal(got[0], wantIndex) || len(wantIndex) != 3 {
			t.Errorf("mention reply index got %v, want one call with %v", got, wantIndex)
		}
		if got := reads.handled(); len(got) != 1 || !slices.Equal(got[0], wantReads) || len(wantReads) != 1 {
			t.Errorf("mention read got %v, want one call with %v", got, wantReads)
		}
		want := allAcked(len(ds))
		want[6] = settlement{terms: 1}
		if got := settlements(ds); !sameSettlements(got, want) {
			t.Errorf("settled %+v, want every event acked and the garbage termed", got)
		}
		if rg.c.Dropped() != 1 || rg.c.Failures() != 0 {
			t.Errorf("dropped %d failures %d, want only the garbage dropped", rg.c.Dropped(), rg.c.Failures())
		}
		rg.stop(t)
	})
}
