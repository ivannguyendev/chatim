package main

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	linkPage    = 10
	replyCID    = "e2e-link-reply-"
	parentCID   = "e2e-link-x"
	mentionCID  = "e2e-link-mention-"
	forwardCID  = "e2e-link-forward-1"
	sinceMargin = time.Minute
)

func (r *linkRun) createGroup() error {
	req := &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e links " + r.st.Room, Members: []string{r.st.User, r.peer}, RequestId: "e2e-links-" + r.st.Room,
	}
	for i := range 2 {
		resp, _, err := r.cl.CreateRoom(r.as(r.st.User), req)
		if err != nil {
			return fmt.Errorf("create group (call %d): %w", i+1, err)
		}
		id, n := resp.GetRoom().GetId(), resp.GetRoom().GetMemberCount()
		if i == 0 {
			r.group = id
			r.watch(id)
		}
		if id != r.group || n != 2 {
			return fmt.Errorf("create group (call %d) gave room %s with %d members, want room %s with 2", i+1, id, n, r.group)
		}
	}
	return r.expect(r.founded(r.group, r.st.User)...)
}

func (r *linkRun) replyTwice() error {
	x, err := r.send(r.group, r.st.User, parentCID, nil)
	if err != nil {
		return err
	}
	r.parent = x
	links := &chatimv1.Message{ReplyTo: &chatimv1.ReplyRef{Seq: x}}
	wants := []e2e.Want{r.sent(r.group, x, nil)}
	for i := range 2 {
		seq, err := r.send(r.group, r.peer, replyCID+strconv.Itoa(i+1), func(req *chatimv1.SendMessageRequest) { req.ReplyTo = links.GetReplyTo() })
		if err != nil {
			return err
		}
		r.replies = append(r.replies, seq)
		wants = append(wants, r.sent(r.group, seq, links))
	}
	wants = append(wants, r.want(r.group, e2e.KindCounts, e2e.RepliesEventID(r.group, x, 2), e2e.RepliesPayload(2)))
	if err := r.expect(wants...); err != nil {
		return err
	}
	return r.checkThread(r.replies)
}

func (r *linkRun) checkThread(want []uint64) error {
	latest, err := page(r.as(r.st.User), r.cl, r.group, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0, linkPage)
	if err != nil {
		return fmt.Errorf("history of the group: %w", err)
	}
	i := slices.IndexFunc(latest, func(m *chatimv1.Message) bool { return m.GetSeq() == r.parent })
	if i < 0 {
		return fmt.Errorf("history of the group misses seq %d", r.parent)
	}
	if err := e2e.CheckReplyCount(latest[i], len(want)); err != nil {
		return err
	}
	resp, _, err := r.cl.GetReplies(r.as(r.st.User), &chatimv1.GetRepliesRequest{RoomId: r.group, Seq: r.parent, Limit: linkPage})
	if err != nil {
		return fmt.Errorf("replies of seq %d: %w", r.parent, err)
	}
	return e2e.CheckReplies(r.group, r.parent, resp.GetMessages(), want)
}

func (r *linkRun) parentIsKept() error {
	_, _, err := r.cl.DeleteMessage(r.as(r.st.User), &chatimv1.DeleteMessageRequest{RoomId: r.group, Seq: r.parent})
	return refused(fmt.Sprintf("delete of seq %d with replies", r.parent), codes.FailedPrecondition, err)
}

func (r *linkRun) deleteOneReply() error {
	last := r.replies[len(r.replies)-1]
	resp, _, err := r.cl.DeleteMessage(r.as(r.peer), &chatimv1.DeleteMessageRequest{RoomId: r.group, Seq: last})
	if err == nil && (!resp.GetMessage().GetDeleted() || resp.GetMessage().GetVer() != 1) {
		err = fmt.Errorf("got %v, want deleted at version 1", resp.GetMessage())
	}
	if err != nil {
		return fmt.Errorf("delete reply seq %d: %w", last, err)
	}
	r.replies = r.replies[:len(r.replies)-1]
	err = r.expect(
		r.want(r.group, e2e.KindDeleted, e2e.ChangeEventID(r.group, last, 1), ""),
		r.want(r.group, e2e.KindCounts, e2e.RepliesEventID(r.group, r.parent, 3), e2e.RepliesPayload(1)),
	)
	if err == nil {
		err = r.checkThread(r.replies)
	}
	if err != nil {
		return err
	}
	return r.parentIsKept()
}

func (r *linkRun) mentionThree() error {
	since := time.Now().Add(-sinceMargin)
	sets := []*chatimv1.MentionSet{
		{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_USER, Id: r.peer}}},
		{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: r.team}}},
		{All: true},
	}
	var wants []e2e.Want
	for i, set := range sets {
		seq, err := r.send(r.group, r.st.User, mentionCID+strconv.Itoa(i+1), func(req *chatimv1.SendMessageRequest) { req.Mentions = set })
		if err != nil {
			return err
		}
		r.mentioned = append([]uint64{seq}, r.mentioned...)
		wants = append(wants, r.sent(r.group, seq, &chatimv1.Message{MentionTargets: set.GetTargets(), MentionAll: set.GetAll()}))
	}
	if err := r.expect(wants...); err != nil {
		return err
	}
	req := &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: r.team, Since: timestamppb.New(since)}}, Limit: linkPage}
	return r.poll("mentions of "+r.peer, func() (error, error) {
		resp, _, err := r.cl.ListMentions(r.as(r.peer), req)
		if err != nil {
			return nil, err
		}
		return e2e.CheckSeqs("mentions of "+r.peer, r.group, resp.GetMessages(), r.mentioned), nil
	})
}

func (r *linkRun) forwardToDirect() error {
	src := e2e.Origin{Room: r.group, Seq: r.replies[0], Author: r.peer, Text: e2e.TextFor(replyCID + "1")}
	ref := &chatimv1.ForwardRef{RoomId: src.Room, Seq: src.Seq}
	seq, err := r.send(r.dm, r.st.User, forwardCID, func(req *chatimv1.SendMessageRequest) { req.ForwardFrom = ref })
	if err != nil {
		return err
	}
	links := &chatimv1.Message{ForwardFrom: &chatimv1.ForwardRef{RoomId: src.Room, Seq: src.Seq, Author: src.Author}}
	if err := r.expect(r.sent(r.dm, seq, links)); err != nil {
		return err
	}
	latest, err := page(r.as(r.peer), r.cl, r.dm, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0, 1)
	if err == nil && (len(latest) != 1 || latest[0].GetSeq() != seq) {
		err = fmt.Errorf("latest page has seq %v, want %d", e2e.SeqsOf(latest), seq)
	}
	if err == nil {
		err = e2e.CheckForward(latest[0], src)
	}
	if err != nil {
		return fmt.Errorf("history of the direct room: %w", err)
	}
	return nil
}

func (r *linkRun) bookmarkMention() error {
	seq := r.mentioned[len(r.mentioned)-1]
	for i, want := range []bool{true, false} {
		resp, _, err := r.cl.SetBookmark(r.as(r.peer), &chatimv1.SetBookmarkRequest{RoomId: r.group, Seq: seq, On: true})
		if err == nil && resp.GetChanged() != want {
			err = fmt.Errorf("changed %v, want %v", resp.GetChanged(), want)
		}
		if err != nil {
			return fmt.Errorf("bookmark seq %d (call %d): %w", seq, i+1, err)
		}
	}
	if err := r.expect(r.want(r.group, e2e.KindBookmark, e2e.BookmarkEventID(r.group, seq, r.peer, 1), e2e.BookmarkPayload(true))); err != nil {
		return err
	}
	resp, _, err := r.cl.ListBookmarks(r.as(r.peer), &chatimv1.ListBookmarksRequest{Limit: linkPage})
	if err != nil {
		return fmt.Errorf("bookmarks of %s: %w", r.peer, err)
	}
	return e2e.CheckBookmarks(r.group, resp.GetItems(), []uint64{seq})
}
