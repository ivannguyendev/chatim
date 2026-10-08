package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	bob   = "e2e-bob"
	carol = "e2e-carol"
)

var errMembersTwice = errors.New("this scenario already ran its member phase")

type memberRun struct {
	base  context.Context
	cl    *route.Client
	st    *e2e.State
	live  *liveFeed
	root  string
	wait  time.Duration
	dm    string
	n     uint64
	wants []e2e.Want
}

func e2eMembers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e members", flag.ContinueOnError)
	o := addOptions(fs)
	live := addLiveFlags(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for the live events of each step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	switch {
	case st.MemberRun:
		return errMembersTwice
	case st.Members < 1 || len(st.Acks) < 2:
		return fmt.Errorf("room %s needs its creation member count and two acked messages, has %d and %d", st.Room, st.Members, len(st.Acks))
	}
	if err := singleTokens(st.Tenant, *live.root); err != nil {
		return err
	}
	feed, err := openLive(*live.url, *live.root+"."+st.Tenant+".*.*.>")
	if err != nil {
		return err
	}
	defer feed.Close()
	r := &memberRun{st: &st, live: feed, root: *live.root, wait: *wait, n: uint64(len(st.Acks))}
	err = withRoutes(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return errors.Join(err, e2e.Save(statePath(*dir), st))
	}
	st.MemberRun = true
	fmt.Fprintf(os.Stderr, "member phase ok on room %s and direct room %s: %d live events checked by id, kind, subject and payload\n", st.Room, r.dm, len(r.wants))
	return e2e.Save(statePath(*dir), st)
}

func (r *memberRun) run() error {
	steps := []func() error{
		r.directRoom, r.addTwo, r.joinedReadsAll, r.promoteBob, r.bobRemovesCarol,
		r.carolIsOut, r.addAgainIsDone, r.unreadThenRead, r.hideAndClear, r.ownerLeaves, r.ownerReturns,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("member step %d: %w", i+1, err)
		}
		fmt.Fprintf(os.Stderr, "member step %d ok\n", i+1)
	}
	return nil
}

func (r *memberRun) as(user string) context.Context {
	return route.WithCaller(r.base, r.st.Tenant, user)
}

func (r *memberRun) want(room, kind, id, payload string) e2e.Want {
	return e2e.Want{ID: id, Kind: kind, Subject: e2e.LiveSubject(r.root, r.st.Tenant, room, kind), Payload: payload}
}

func (r *memberRun) member(room, kind, user string, ver uint32, payload string) e2e.Want {
	return r.want(room, kind, e2e.MemberEventID(room, user, ver), payload)
}

func (r *memberRun) count(ver uint64, n int32) e2e.Want {
	return r.want(r.st.Room, e2e.KindMemberCount, e2e.MemberCountEventID(r.st.Room, ver), e2e.CountPayload(n))
}

func (r *memberRun) expect(ws ...e2e.Want) error {
	r.wants = append(r.wants, ws...)
	rooms := []string{r.st.Room}
	if r.dm != "" {
		rooms = append(rooms, r.dm)
	}
	return r.live.await(r.base, rooms, r.wants, r.wait)
}

func refused(what string, want codes.Code, err error) error {
	switch got := status.Code(err); {
	case err == nil:
		return fmt.Errorf("%s succeeded, want %s", what, want)
	case got != want:
		return fmt.Errorf("%s returned %s, want %s: %w", what, got, want, err)
	default:
		return nil
	}
}
