package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReadUpdatedCarriesTheReaderAndItsReadVer(t *testing.T) {
	want := memberEnvelope("9007199254740993-rd-bob-v6", "bob")
	want.Payload = &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "bob", ReadSeq: 41, ReadVer: 6}}
	got := pbconv.ReadUpdated(groupRoom(), "bob", domain.ReadPosition{Seq: 41, Ver: 6}, changedAt)
	if !proto.Equal(got, want) {
		t.Fatalf("ReadUpdated = %v, want %v", got, want)
	}
}

func TestHiddenAndClearedEventsCarryTheUserAndTheirIDs(t *testing.T) {
	clearedAt := sentAt.Add(3_123_000_000)
	wantHidden := memberEnvelope("9007199254740993-hd-bob-3-9", "bob")
	wantHidden.ThreadRoot, wantHidden.Seq = 3, 9
	wantHidden.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: "bob", ThreadRoot: 3, Seq: 9}}
	if got := pbconv.MessageHidden(groupRoom(), "bob", 3, 9, changedAt); !proto.Equal(got, wantHidden) {
		t.Fatalf("MessageHidden = %v, want %v", got, wantHidden)
	}
	wantCleared := memberEnvelope(pbconv.ClearedEventID(groupRoom().ID, "bob", clearedAt), "bob")
	wantCleared.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: "bob", ClearedAt: timestamppb.New(clearedAt)}}
	if got := pbconv.HistoryCleared(groupRoom(), "bob", clearedAt, changedAt); !proto.Equal(got, wantCleared) {
		t.Fatalf("HistoryCleared = %v, want %v", got, wantCleared)
	}
}
