package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var (
	memberRoles = map[string]chatimv1.MemberRole{
		"owner":  chatimv1.MemberRole_MEMBER_ROLE_OWNER,
		"admin":  chatimv1.MemberRole_MEMBER_ROLE_ADMIN,
		"member": chatimv1.MemberRole_MEMBER_ROLE_MEMBER,
	}
	errTargetRequired = errors.New("-target is required")
)

type roomCall func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error)

func reply[T proto.Message](resp T, st route.Stats, err error) (proto.Message, route.Stats, error) {
	return resp, st, err
}

func inRoomCmd(ctx context.Context, fs *flag.FlagSet, args []string, check func() error, call roomCall) error {
	o := addOptions(fs)
	room := fs.String("room", "", "room id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *room == "" {
		return errRoomRequired
	}
	if err := check(); err != nil {
		return err
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := call(ctx, s.client, *room)
		if err != nil {
			return err
		}
		report(fs.Name(), st)
		return printJSON(resp)
	})
}

func noCheck() error { return nil }

func needTarget(target *string) func() error {
	return func() error {
		if *target == "" {
			return errTargetRequired
		}
		return nil
	}
}

func addMembersCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("add-members", flag.ContinueOnError)
	users := fs.String("users", "", "comma-separated users to add")
	requestID := fs.String("request-id", "", "request id, random when empty; reuse it to retry exactly once")
	check := func() error {
		if len(splitList(*users)) == 0 {
			return errors.New("-users is required")
		}
		if *requestID == "" {
			*requestID = randomCID()
		}
		return nil
	}
	return inRoomCmd(ctx, fs, args, check, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: room, Users: splitList(*users), RequestId: *requestID}))
	})
}

func removeMemberCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("remove-member", flag.ContinueOnError)
	target := fs.String("target", "", "user to remove")
	return inRoomCmd(ctx, fs, args, needTarget(target), func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: room, User: *target}))
	})
}

func leaveCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("leave", flag.ContinueOnError)
	return inRoomCmd(ctx, fs, args, noCheck, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: room}))
	})
}

func setRoleCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("set-role", flag.ContinueOnError)
	target := fs.String("target", "", "user whose role changes")
	name := fs.String("role", "", "new role: owner, admin or member")
	check := func() error {
		if _, ok := memberRoles[*name]; !ok {
			return fmt.Errorf("-role %q: want owner, admin or member", *name)
		}
		return needTarget(target)()
	}
	return inRoomCmd(ctx, fs, args, check, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: *target, Role: memberRoles[*name]}))
	})
}

func setPriorityCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("set-priority", flag.ContinueOnError)
	target := fs.String("target", "", "user whose priority changes")
	priority := fs.Int64("priority", 0, "new priority")
	var value int32
	check := func() error {
		var err error
		if value, err = toInt32("priority", *priority); err != nil {
			return err
		}
		return needTarget(target)()
	}
	return inRoomCmd(ctx, fs, args, check, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.SetMemberPriority(ctx, &chatimv1.SetMemberPriorityRequest{RoomId: room, User: *target, Priority: value}))
	})
}

func toInt32(name string, v int64) (int32, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fmt.Errorf("-%s %d: want a 32-bit integer", name, v)
	}
	return int32(v), nil
}

func readCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	seq := fs.Uint64("seq", 0, "last read seq, 0 for the last message")
	return inRoomCmd(ctx, fs, args, noCheck, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: room, Seq: *seq}))
	})
}

func unreadCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unread", flag.ContinueOnError)
	seq := fs.Uint64("seq", 0, "first unread seq")
	check := func() error {
		if *seq == 0 {
			return errSeqRequired
		}
		return nil
	}
	return inRoomCmd(ctx, fs, args, check, func(ctx context.Context, cl *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(cl.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: *seq}))
	})
}
