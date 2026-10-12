package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHistoryHidesASendStoredTwice(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	id, err := strconv.ParseUint(room, 10, 64)
	if err != nil {
		t.Fatalf("room id %q: %v", room, err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	stored := func(seq uint64, cid string) domain.Message {
		return domain.Message{Room: id, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: cid, CreatedAt: at}
	}
	for _, r := range rg.msgs.Insert(t.Context(), []domain.Message{stored(1, "c-1"), stored(2, "c-1"), stored(3, "c-2")}) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert: %v %v", r.Outcome, r.Err)
		}
	}
	resp, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	var got []uint64
	for _, m := range resp.GetMessages() {
		got = append(got, m.GetSeq())
	}
	if !slices.Equal(got, []uint64{1, 3}) {
		t.Fatalf("seqs = %v, want [1 3]", got)
	}
}

func TestHistoryAsksThePolicy(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	rg := newRig(t, options{policy: deny})
	room := rg.createGroup(t, "acme", "alice")
	_, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	if got.Action != access.ReadHistory || got.User != "alice" || got.Member.User != "alice" {
		t.Fatalf("policy saw %+v, want read_history by member alice", got)
	}
}
