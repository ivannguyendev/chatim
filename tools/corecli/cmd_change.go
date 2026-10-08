package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const maxEditPage = 100

var errSeqRequired = errors.New("-seq is required")

type messageFlags struct {
	room   *string
	thread *uint64
	seq    *uint64
}

func addMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		room:   fs.String("room", "", "room id"),
		thread: fs.Uint64("thread", 0, "thread root, 0 for the main timeline"),
		seq:    fs.Uint64("seq", 0, "message seq"),
	}
}

func parseMessage(fs *flag.FlagSet, args []string, msg messageFlags) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *msg.room == "":
		return errRoomRequired
	case *msg.seq == 0:
		return errSeqRequired
	default:
		return nil
	}
}

func toUint32(name string, v uint64) (uint32, error) {
	if v > math.MaxUint32 {
		return 0, fmt.Errorf("-%s %d: want at most %d", name, v, uint64(math.MaxUint32))
	}
	return uint32(v), nil
}

func editCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	base := fs.Uint64("base", 0, "version the caller saw, 0 for the original message")
	text := fs.String("text", "", "new text")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	baseVersion, err := toUint32("base", *base)
	if err != nil {
		return err
	}
	req := &chatimv1.EditMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, BaseVer: baseVersion, Text: *text}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.EditMessage(ctx, req)
		if err != nil {
			return err
		}
		report("edit", st)
		return printJSON(resp.GetMessage())
	})
}

func deleteCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	base := fs.Uint64("base", 0, "version the caller saw, 0 for the original message")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	baseVersion, err := toUint32("base", *base)
	if err != nil {
		return err
	}
	req := &chatimv1.DeleteMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, BaseVer: baseVersion}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.DeleteMessage(ctx, req)
		if err != nil {
			return err
		}
		report("delete", st)
		return printJSON(resp.GetMessage())
	})
}

func hideCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hide", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	req := &chatimv1.HideMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.HideMessage(ctx, req)
		if err != nil {
			return err
		}
		report("hide", st)
		return printJSON(resp)
	})
}

func clearCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("clear", flag.ContinueOnError)
	o := addOptions(fs)
	room := fs.String("room", "", "room id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *room == "" {
		return errRoomRequired
	}
	req := &chatimv1.ClearHistoryRequest{RoomId: *room}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.ClearHistory(ctx, req)
		if err != nil {
			return err
		}
		report("clear", st)
		return printJSON(resp)
	})
}

func editsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edits", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	after := fs.Uint64("after", 0, "list versions after this one")
	limit := fs.Uint64("limit", maxEditPage, "versions per call, at most 100")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	if *limit < 1 || *limit > maxEditPage {
		return fmt.Errorf("-limit %d: want 1..%d", *limit, maxEditPage)
	}
	afterVersion, err := toUint32("after", *after)
	if err != nil {
		return err
	}
	size, err := toUint32("limit", *limit)
	if err != nil {
		return err
	}
	req := &chatimv1.GetEditHistoryRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, AfterVer: afterVersion, Limit: size}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.GetEditHistory(ctx, req)
		if err != nil {
			return err
		}
		report("edits", st)
		for _, v := range resp.GetVersions() {
			if err := printJSON(v); err != nil {
				return err
			}
		}
		return nil
	})
}
