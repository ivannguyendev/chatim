package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

var errLinksTwice = errors.New("this scenario already ran its link phase")

type linkRun struct {
	*memberRun
	peer      string
	group     string
	team      string
	parent    uint64
	replies   []uint64
	mentioned []uint64
}

func e2eLinks(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e links", flag.ContinueOnError)
	o := addOptions(fs)
	live := addLiveFlags(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for the live events and indexes of each step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if st.LinkRun {
		return errLinksTwice
	}
	if err := singleTokens(st.Tenant, *live.root); err != nil {
		return err
	}
	feed, err := openLive(*live.url, *live.root+"."+st.Tenant+".*.*.>")
	if err != nil {
		return err
	}
	defer feed.Close()
	r := &linkRun{
		memberRun: &memberRun{st: &st, live: feed, root: *live.root, wait: *wait},
		peer:      "e2e-peer-" + st.Room,
		team:      "e2e-team-" + st.Room,
	}
	err = withRoutes(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	st.LinkRun = true
	fmt.Fprintf(os.Stderr, "link phase ok on group %s and direct room %s: %d live events checked by id, kind, subject and payload\n", r.group, r.dm, len(r.wants))
	return e2e.Save(statePath(*dir), st)
}

func (r *linkRun) run() error {
	steps := []func() error{
		r.openDirectTwice, r.reactInDirect, r.createGroup, r.replyTwice, r.parentIsKept,
		r.deleteOneReply, r.mentionThree, r.forwardToDirect, r.bookmarkMention,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("link step %d: %w", i+1, err)
		}
		fmt.Fprintf(os.Stderr, "link step %d ok\n", i+1)
	}
	return nil
}

func (r *linkRun) watch(room string) { r.rooms = append(r.rooms, room) }

func (r *linkRun) founded(room, creator string) []e2e.Want {
	return []e2e.Want{
		r.want(room, e2e.KindRoomCreated, e2e.RoomCreatedEventID(room), ""),
		r.member(room, e2e.KindMemberAdded, creator, 1, ""),
		r.member(room, e2e.KindMemberAdded, r.peer, 1, ""),
		r.want(room, e2e.KindMemberCount, e2e.MemberCountEventID(room, 1), e2e.CountPayload(2)),
	}
}

func (r *linkRun) openDirect(caller, other string) (*chatimv1.OpenDirectRoomResponse, error) {
	resp, _, err := r.cl.OpenDirectRoom(r.as(caller), &chatimv1.OpenDirectRoomRequest{OtherUser: other})
	if err != nil {
		return nil, fmt.Errorf("open direct room as %s with %s: %w", caller, other, err)
	}
	return resp, nil
}

func (r *linkRun) openDirectTwice() error {
	user := r.st.User
	first, err := r.openDirect(user, r.peer)
	if err != nil {
		return err
	}
	if !first.GetCreated() || first.GetRoom().GetType() != chatimv1.RoomType_ROOM_TYPE_DM {
		return fmt.Errorf("first open gave room %v created %v, want a new direct room", first.GetRoom(), first.GetCreated())
	}
	r.dm = first.GetRoom().GetId()
	r.watch(r.dm)
	for _, pair := range [][2]string{{user, r.peer}, {r.peer, user}} {
		again, err := r.openDirect(pair[0], pair[1])
		if err != nil {
			return err
		}
		if again.GetRoom().GetId() != r.dm || again.GetCreated() {
			return fmt.Errorf("open as %s gave room %s created %v, want room %s already open", pair[0], again.GetRoom().GetId(), again.GetCreated(), r.dm)
		}
	}
	return r.expect(r.founded(r.dm, user)...)
}

func (r *linkRun) send(room, user, cid string, edit func(*chatimv1.SendMessageRequest)) (uint64, error) {
	req := &chatimv1.SendMessageRequest{RoomId: room, Cid: cid, Text: e2e.TextFor(cid)}
	if edit != nil {
		edit(req)
	}
	resp, _, err := r.cl.SendMessage(r.as(user), req)
	if err != nil {
		return 0, fmt.Errorf("send %s to room %s as %s: %w", cid, room, user, err)
	}
	return resp.GetSeq(), nil
}

func (r *linkRun) sent(room string, seq uint64, links *chatimv1.Message) e2e.Want {
	return r.want(room, e2e.KindCreated, e2e.MessageEventID(room, seq), e2e.LinksPayload(links))
}

func (r *linkRun) reactInDirect() error {
	seq, err := r.send(r.dm, r.st.User, "e2e-link-dm-1", nil)
	if err != nil {
		return err
	}
	emojis, err := reactionEmojis(r.as(r.peer), r.cl)
	if err != nil {
		return err
	}
	emoji := emojis[0]
	resp, _, err := r.cl.ReactMessage(r.as(r.peer), &chatimv1.ReactMessageRequest{RoomId: r.dm, Seq: seq, Emoji: emoji})
	want := e2e.Reaction{Seq: seq, Emoji: emoji, Change: 1, Version: 1}
	if err == nil {
		err = e2e.CheckReactReply(want, resp.GetChange(), resp.GetReactions())
	}
	if err != nil {
		return fmt.Errorf("react on direct seq %d: %w", seq, err)
	}
	return r.expect(
		r.sent(r.dm, seq, nil),
		r.want(r.dm, e2e.KindReaction, e2e.ReactionEventID(r.dm, seq, r.peer, 1), emoji),
		r.want(r.dm, e2e.KindCounts, e2e.CountsEventID(r.dm, seq, 1), emoji+"=1"),
	)
}

func (r *linkRun) poll(what string, check func() (pending, err error)) error {
	ctx, cancel := context.WithTimeout(r.base, r.wait)
	defer cancel()
	for {
		pending, err := check()
		switch {
		case err != nil:
			return fmt.Errorf("%s: %w", what, err)
		case pending == nil:
			return nil
		case !backoff.Pause(ctx, eventPoll):
			return fmt.Errorf("%s after %v: %w", what, r.wait, pending)
		}
	}
}
