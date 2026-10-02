package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	roomTypes = map[string]chatimv1.RoomType{"group": chatimv1.RoomType_ROOM_TYPE_GROUP, "dm": chatimv1.RoomType_ROOM_TYPE_DM}
	anchors   = map[string]chatimv1.HistoryAnchor{
		"latest": chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST,
		"oldest": chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST,
		"before": chatimv1.HistoryAnchor_HISTORY_ANCHOR_BEFORE,
		"after":  chatimv1.HistoryAnchor_HISTORY_ANCHOR_AFTER,
	}
	errRoomRequired = errors.New("-room is required")
)

func createRoomCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("create-room", flag.ContinueOnError)
	o := addOptions(fs)
	typ := fs.String("type", "group", "room type: group or dm")
	name := fs.String("name", "", "room name, required for a group")
	members := fs.String("members", "", "comma-separated members besides the caller")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rt, ok := roomTypes[*typ]
	if !ok {
		return fmt.Errorf("-type %q: want group or dm", *typ)
	}
	req := &chatimv1.CreateRoomRequest{Type: rt, Name: *name, Members: append([]string{o.user}, splitList(*members)...)}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.CreateRoom(ctx, req)
		if err != nil {
			return err
		}
		report("create-room", st)
		return printJSON(resp.GetRoom())
	})
}

func sendCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	o := addOptions(fs)
	room := fs.String("room", "", "room id")
	cid := fs.String("cid", "", "client message id, random when empty; reuse it to retry exactly once")
	text := fs.String("text", "", "message text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *room == "" {
		return errRoomRequired
	}
	if *cid == "" {
		*cid = randomCID()
	}
	req := &chatimv1.SendMessageRequest{RoomId: *room, Cid: *cid, Text: *text}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.SendMessage(ctx, req)
		if err != nil {
			return err
		}
		report("send cid "+*cid, st)
		return printJSON(resp)
	})
}

func historyCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	o := addOptions(fs)
	room := fs.String("room", "", "room id")
	anchor := fs.String("anchor", "latest", "latest, oldest, before or after")
	seq := fs.Uint64("seq", 0, "anchor seq for before and after")
	limit := fs.Int("limit", 50, "page size, at most 100")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, ok := anchors[*anchor]
	switch {
	case *room == "":
		return errRoomRequired
	case !ok:
		return fmt.Errorf("-anchor %q: want latest, oldest, before or after", *anchor)
	case *limit < 1 || *limit > 100:
		return fmt.Errorf("-limit %d: want 1..100", *limit)
	}
	req := &chatimv1.GetHistoryRequest{RoomId: *room, Anchor: a, Seq: *seq, Limit: int32(*limit)}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.GetHistory(ctx, req)
		if err != nil {
			return err
		}
		report("history", st)
		for _, m := range resp.GetMessages() {
			if err := printJSON(m); err != nil {
				return err
			}
		}
		return nil
	})
}

func printJSON(m proto.Message) error {
	data, err := protojson.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %T: %w", m, err)
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func randomCID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "cli-" + hex.EncodeToString(b[:])
}
