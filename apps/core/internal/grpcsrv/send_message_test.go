package grpcsrv_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/resilience"
)

func TestSendMessageHandsTheCallerAndRequestToTheSender(t *testing.T) {
	at := time.UnixMilli(1_700_000_000_123).UTC()
	sender := &fakeSender{ack: actor.Ack{Seq: 7, CreatedAt: at}}
	rg := newRig(t, options{sender: sender})
	resp := rg.send(t, as(t, "acme", "alice"), "9007199254740993", "c-1", "xin chào")
	want := &chatimv1.SendMessageResponse{Seq: 7, CreatedAt: timestamppb.New(at)}
	if !proto.Equal(resp, want) {
		t.Fatalf("response = %v, want %v", resp, want)
	}
	cmds := sender.sent()
	wantCmd := actor.SendCmd{Tenant: "acme", User: "alice", Room: 9_007_199_254_740_993, CID: "c-1", Text: "xin chào"}
	if len(cmds) != 1 || cmds[0] != wantCmd {
		t.Fatalf("sender got %+v, want [%+v]", cmds, wantCmd)
	}
}

func TestSendMessageMapsSenderFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{"mailbox full", fmt.Errorf("room 42 mailbox of 16 full: %w", domain.ErrBusy), codes.ResourceExhausted},
		{"retry later", fmt.Errorf("flusher closed on core-7: %w", domain.ErrRetryLater), codes.Unavailable},
		{"room missing", domain.ErrRoomNotFound, codes.NotFound},
		{"not a member", domain.ErrNotMember, codes.PermissionDenied},
		{"unexpected", errors.New("mongo 10.0.0.7:27017: connection reset"), codes.Internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rg := newRig(t, options{sender: &fakeSender{err: c.err}})
			_, err := rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c-1", Text: "hi"})
			expectCode(t, err, c.code)
		})
	}
}

func TestSendMessageIsShedWhenTheServerIsFull(t *testing.T) {
	limiter := resilience.NewLimiter(1, 0)
	if err := limiter.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer limiter.Release()
	sender := &fakeSender{}
	rg := newRig(t, options{sender: sender, limiter: limiter})
	_, err := rg.client.SendMessage(as(t, "acme", "alice"), &chatimv1.SendMessageRequest{RoomId: "42", Cid: "c-1", Text: "hi"})
	if code := status.Code(err); code != codes.Unavailable {
		t.Fatalf("overloaded send = %v, want Unavailable", err)
	}
	if len(sender.sent()) != 0 {
		t.Fatal("shed request reached the sender")
	}
}

func TestSendMessageRejectsBadInput(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	cases := map[string]*chatimv1.SendMessageRequest{
		"empty room id":       {RoomId: "", Cid: "c-1", Text: "hi"},
		"room id not decimal": {RoomId: "abc", Cid: "c-1", Text: "hi"},
		"room id zero":        {RoomId: "0", Cid: "c-1", Text: "hi"},
		"room id negative":    {RoomId: "-1", Cid: "c-1", Text: "hi"},
		"room id over 63 bit": {RoomId: "9223372036854775808", Cid: "c-1", Text: "hi"},
		"thread reply":        {RoomId: room, ThreadRoot: 7, Cid: "c-1", Text: "hi"},
		"empty cid":           {RoomId: room, Text: "hi"},
		"cid with a dot":      {RoomId: room, Cid: "c.1", Text: "hi"},
		"blank text":          {RoomId: room, Cid: "c-1", Text: " \n\t"},
		"text over 16KB":      {RoomId: room, Cid: "c-1", Text: strings.Repeat("a", 16385)},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "acme", "alice"), req)
			expectCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestSendMessageHidesRoomsOfOtherTenants(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	for name, req := range map[string]*chatimv1.SendMessageRequest{
		"other tenant": {RoomId: room, Cid: "c-1", Text: "hi"},
		"unknown room": {RoomId: "4242", Cid: "c-2", Text: "hi"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := rg.client.SendMessage(as(t, "other", "alice"), req)
			expectCode(t, err, codes.NotFound)
		})
	}
}

func TestSendMessageRequiresMembership(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	_, err := rg.client.SendMessage(as(t, "acme", "mallory"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "c-1", Text: "hi"})
	expectCode(t, err, codes.PermissionDenied)
	resp := rg.send(t, as(t, "acme", "bob"), room, "c-1", "hi")
	if resp.GetSeq() != 1 || resp.GetCreatedAt() == nil {
		t.Fatalf("member send = %v, want seq 1 with a timestamp", resp)
	}
}
