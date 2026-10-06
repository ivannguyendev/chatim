package main

import (
	"context"
	"flag"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func reactCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	emoji := fs.String("emoji", "", "emoji to set; empty removes the caller's reaction")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	req := &chatimv1.ReactMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, Emoji: *emoji}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.ReactMessage(ctx, req)
		if err != nil {
			return err
		}
		report("react", st)
		return printJSON(resp)
	})
}

func pinCmd(ctx context.Context, args []string) error { return pinChangeCmd(ctx, "pin", args) }

func unpinCmd(ctx context.Context, args []string) error { return pinChangeCmd(ctx, "unpin", args) }

func pinChangeCmd(ctx context.Context, name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := setPinned(ctx, s.client, name == "pin", *msg.room, *msg.thread, *msg.seq)
		if err != nil {
			return err
		}
		report(name, st)
		return printJSON(resp)
	})
}

func setPinned(ctx context.Context, cl *route.Client, pinned bool, room string, thread, seq uint64) (proto.Message, route.Stats, error) {
	if pinned {
		return cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, ThreadRoot: thread, Seq: seq})
	}
	return cl.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: room, ThreadRoot: thread, Seq: seq})
}
