package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	firstEmoji  = "👍"
	secondEmoji = "🎉"
)

var errMarkedTwice = errors.New("this scenario already holds its reaction and pin")

func e2eReactPin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e react-pin", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	react := fs.Uint64("react", 3, "acked, not deleted seq to react to, then change the emoji")
	pin := fs.Uint64("pin", 4, "acked, not deleted seq to pin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	switch {
	case len(st.Reactions) > 0 || len(st.Pins) > 0:
		return errMarkedTwice
	case !markable(st, *react) || !markable(st, *pin):
		return fmt.Errorf("-react %d and -pin %d must be acked seq that are not deleted", *react, *pin)
	}
	o.tenant, o.user = st.Tenant, st.User
	var r e2e.Reaction
	var p e2e.Pin
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		var err error
		if r, err = reactThenChange(ctx, s.client, st.Room, *react); err != nil {
			return err
		}
		if p, err = pinTwice(ctx, s.client, st.Room, st.User, *pin); err != nil {
			return err
		}
		return deletedTakesNoMark(ctx, s.client, st)
	})
	if err != nil {
		return err
	}
	st.Reactions, st.Pins = []e2e.Reaction{r}, []e2e.Pin{p}
	fmt.Fprintf(os.Stderr, "seq %d reacted %s then %s (change %d, counts version %d), seq %d pinned (pin version %d); repeats were no-ops\n",
		r.Seq, firstEmoji, r.Emoji, r.Change, r.Version, p.Seq, p.Version)
	return e2e.Save(statePath(*dir), st)
}

func markable(st e2e.State, seq uint64) bool {
	acked := slices.ContainsFunc(st.Acks, func(a e2e.Ack) bool { return a.Seq == seq })
	deleted := slices.ContainsFunc(st.Changes, func(c e2e.Change) bool { return c.Seq == seq && c.Deleted })
	return acked && !deleted
}

func reactThenChange(ctx context.Context, cl *route.Client, room string, seq uint64) (e2e.Reaction, error) {
	steps := []e2e.Reaction{
		{Seq: seq, Emoji: firstEmoji, Change: 1, Version: 1},
		{Seq: seq, Emoji: secondEmoji, Change: 2, Version: 2},
		{Seq: seq, Emoji: secondEmoji, Change: 2, Version: 2},
	}
	for i, want := range steps {
		resp, stats, err := cl.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: want.Emoji})
		if err == nil {
			err = e2e.CheckReactReply(want, resp.GetChange(), resp.GetReactions())
		}
		if err != nil {
			return e2e.Reaction{}, fmt.Errorf("react %d on seq %d: %w", i+1, seq, err)
		}
		report("react "+strconv.Itoa(i+1)+" seq "+strconv.FormatUint(seq, 10), stats)
	}
	return steps[len(steps)-1], nil
}

func pinTwice(ctx context.Context, cl *route.Client, room, user string, seq uint64) (e2e.Pin, error) {
	want := e2e.Pin{Seq: seq, Version: 1}
	for i := range 2 {
		resp, stats, err := cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, Seq: seq})
		if err == nil {
			err = e2e.CheckPinReply(want, user, resp.GetPinVersion(), resp.GetPins())
		}
		if err != nil {
			return e2e.Pin{}, fmt.Errorf("pin %d of seq %d: %w", i+1, seq, err)
		}
		report("pin "+strconv.Itoa(i+1)+" seq "+strconv.FormatUint(seq, 10), stats)
	}
	return want, nil
}

func deletedTakesNoMark(ctx context.Context, cl *route.Client, st e2e.State) error {
	for _, c := range st.Changes {
		if !c.Deleted {
			continue
		}
		_, _, err := cl.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: st.Room, Seq: c.Seq, Emoji: firstEmoji})
		if status.Code(err) != codes.FailedPrecondition {
			return fmt.Errorf("react on deleted seq %d = %w, want FailedPrecondition", c.Seq, err)
		}
		_, _, err = cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: st.Room, Seq: c.Seq})
		if status.Code(err) != codes.FailedPrecondition {
			return fmt.Errorf("pin of deleted seq %d = %w, want FailedPrecondition", c.Seq, err)
		}
	}
	return nil
}
