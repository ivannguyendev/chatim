package command_test

import (
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMarkReadGoesToCoreAsTheTokenUserWithoutUnknownFields(t *testing.T) {
	h := newHarness(t, time.Second)
	h.core.reply(&chatimv1.MarkReadResponse{ReadSeq: 7, ReadVer: 3})
	mark := &chatimv1.MarkReadRequest{RoomId: "42", Seq: 7}
	mark.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	ctx := metadata.AppendToOutgoingContext(t.Context(), callerid.TenantHeader, "evil", callerid.UserHeader, "mallory")
	req := &chatimv1.ClientRequest{Id: 4, Request: &chatimv1.ClientRequest_MarkRead{MarkRead: mark}}
	resp, outcome := h.exec.Execute(ctx, testClaims, req)
	if resp.GetCode() != 0 || outcome.Command != "MarkRead" || resp.GetMarkRead().GetReadSeq() != 7 || resp.GetMarkRead().GetReadVer() != 3 {
		t.Fatalf("resp = %v, outcome = %+v", resp, outcome)
	}
	calls := h.core.recorded()
	if len(calls) != 1 || calls[0].method != "/chatim.v1.CoreService/MarkRead" || len(h.router.routes) != 1 || h.router.routes[0] != "room:42" {
		t.Fatalf("core calls = %+v, routes = %v", calls, h.router.routes)
	}
	if tenant, user := calls[0].md.Get(callerid.TenantHeader), calls[0].md.Get(callerid.UserHeader); len(tenant) != 1 || tenant[0] != "t1" || len(user) != 1 || user[0] != "u1" {
		t.Fatalf("caller = %v/%v, want t1/u1 from the token", tenant, user)
	}
	var got chatimv1.MarkReadRequest
	if err := proto.Unmarshal(calls[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ProtoReflect().GetUnknown()) != 0 || got.GetRoomId() != "42" || got.GetSeq() != 7 {
		t.Fatalf("core got %v with unknown %x, want room 42 seq 7 only", &got, got.ProtoReflect().GetUnknown())
	}
}
