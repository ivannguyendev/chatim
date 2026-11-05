package main

import (
	"context"
	"errors"
	"flag"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var (
	errGroupRequired = errors.New("-group and -name are required")
	errVerRequired   = errors.New("-ver must be at least 1")
)

type anyCall func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error)

func anyCoreCmd(ctx context.Context, fs *flag.FlagSet, o *options, args []string, check func() error, call anyCall) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := call(ctx, s.client)
		if err != nil {
			return err
		}
		report(fs.Name(), st)
		return printJSON(resp)
	})
}

func setUserGroupsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("set-user-groups", flag.ContinueOnError)
	o := addOptions(fs)
	groups := fs.String("groups", "", "comma-separated groups of the user, every group the user is in (empty: none)")
	ver := fs.Uint64("ver", 0, "version of the list from the user system; an older or equal one changes nothing")
	check := func() error {
		if *ver == 0 {
			return errVerRequired
		}
		return nil
	}
	return anyCoreCmd(ctx, fs, o, args, check, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		item := &chatimv1.UserGroupsItem{User: o.user, Groups: splitList(*groups), Ver: *ver}
		return reply(cl.SetUserGroups(ctx, &chatimv1.SetUserGroupsRequest{Items: []*chatimv1.UserGroupsItem{item}}))
	})
}

func setGroupsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("set-groups", flag.ContinueOnError)
	o := addOptions(fs)
	group := fs.String("group", "", "group id")
	name := fs.String("name", "", "group name shown in the @ picker")
	inactive := fs.Bool("inactive", false, "keep the group out of notifications and lists")
	ver := fs.Uint64("ver", 0, "version of the group from the group system; an older or equal one changes nothing")
	check := func() error {
		switch {
		case *group == "" || *name == "":
			return errGroupRequired
		case *ver == 0:
			return errVerRequired
		}
		return nil
	}
	return anyCoreCmd(ctx, fs, o, args, check, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		item := &chatimv1.GroupItem{Id: *group, Name: *name, Active: !*inactive, Ver: *ver}
		return reply(cl.SetGroups(ctx, &chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{item}}))
	})
}

func roomGroupsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("room-groups", flag.ContinueOnError)
	return inRoomCmd(ctx, fs, args, noCheck, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.ListRoomGroups(ctx, &chatimv1.ListRoomGroupsRequest{RoomId: room}))
	})
}
