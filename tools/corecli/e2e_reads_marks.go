package main

import (
	"bytes"
	"fmt"
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const forwardMeta = "e2e-forward"

func (r *roomRun) reactorsListed() error {
	room := r.list[3]
	emojis, err := reactionEmojis(r.as(r.me), r.cl)
	if err != nil {
		return err
	}
	for i, user := range []string{r.peer, r.me} {
		if _, _, err := r.cl.ReactMessage(r.as(user), &chatimv1.ReactMessageRequest{RoomId: room, Seq: r.parent, Emoji: emojis[i]}); err != nil {
			return fmt.Errorf("react %s on seq %d as %s: %w", emojis[i], r.parent, user, err)
		}
	}
	cases := []struct {
		emoji string
		want  []string
	}{
		{"", slices.Sorted(slices.Values([]string{r.peer + "=" + emojis[0], r.me + "=" + emojis[1]}))},
		{emojis[0], []string{r.peer + "=" + emojis[0]}},
	}
	for _, c := range cases {
		resp, _, err := r.cl.GetReactions(r.as(r.me), &chatimv1.GetReactionsRequest{RoomId: room, Seq: r.parent, Emoji: c.emoji})
		if err != nil {
			return fmt.Errorf("reactions %q of seq %d: %w", c.emoji, r.parent, err)
		}
		got := make([]string, len(resp.GetReactors()))
		for i, x := range resp.GetReactors() {
			got[i] = x.GetUser() + "=" + x.GetEmoji()
		}
		if c.emoji == "" {
			slices.Sort(got)
		}
		if !slices.Equal(got, c.want) {
			return fmt.Errorf("reactions %q of seq %d = %v, want %v", c.emoji, r.parent, got, c.want)
		}
	}
	return nil
}

func (r *roomRun) pinsNewestFirst() error {
	room := r.list[3]
	for _, seq := range []uint64{r.parent, r.replies[0]} {
		if _, _, err := r.cl.PinMessage(r.as(r.me), &chatimv1.PinMessageRequest{RoomId: room, Seq: seq}); err != nil {
			return fmt.Errorf("pin seq %d: %w", seq, err)
		}
	}
	resp, _, err := r.cl.ListPins(r.as(r.peer), &chatimv1.ListPinsRequest{RoomId: room})
	if err != nil {
		return fmt.Errorf("pins of room %s: %w", room, err)
	}
	msgs := make([]*chatimv1.Message, len(resp.GetItems()))
	for i, it := range resp.GetItems() {
		if it.GetPin().GetSeq() != it.GetMessage().GetSeq() {
			return fmt.Errorf("pin %v carries message seq %d", it.GetPin(), it.GetMessage().GetSeq())
		}
		msgs[i] = it.GetMessage()
	}
	if resp.GetPinVer() != 2 {
		return fmt.Errorf("pins of room %s at pin_ver %d, want 2", room, resp.GetPinVer())
	}
	return e2e.CheckSeqs("pins", room, msgs, []uint64{r.replies[0], r.parent})
}

func (r *roomRun) membersOwnerFirst() error {
	room := r.list[3]
	resp, _, err := r.cl.ListMembers(r.as(r.peer), &chatimv1.ListMembersRequest{RoomId: room})
	if err != nil {
		return fmt.Errorf("members of room %s: %w", room, err)
	}
	got := make([]string, len(resp.GetMembers()))
	for i, m := range resp.GetMembers() {
		got[i] = m.GetUser() + ":" + e2e.RoleName(m.GetRole())
	}
	if want := []string{r.me + ":owner", r.peer + ":member"}; !slices.Equal(got, want) || resp.GetNext() != "" {
		return fmt.Errorf("members of room %s = %v next %q, want %v and the end", room, got, resp.GetNext(), want)
	}
	return nil
}

func (r *roomRun) forwardKeepsExtras() error {
	room, src := r.list[3], r.list[0]
	ref := &chatimv1.ForwardRef{RoomId: src, Seq: 1}
	seq, err := r.send(room, r.peer, roomsCID+"forward", func(req *chatimv1.SendMessageRequest) {
		req.ForwardFrom, req.SkipUnread, req.Meta = ref, true, []byte(forwardMeta)
	})
	if err != nil {
		return err
	}
	extras := &chatimv1.Message{ForwardFrom: &chatimv1.ForwardRef{RoomId: src, Seq: 1, Author: r.peer}, SkipUnread: true, Meta: []byte(forwardMeta)}
	if err := r.expect(r.sent(room, seq, extras)); err != nil {
		return err
	}
	resp, _, err := r.cl.GetMessages(r.as(r.me), &chatimv1.GetMessagesRequest{RoomId: room, Seqs: []uint64{seq}})
	if err != nil {
		return fmt.Errorf("forward seq %d: %w", seq, err)
	}
	if err := e2e.CheckSeqs("forward", room, resp.GetMessages(), []uint64{seq}); err != nil {
		return err
	}
	got := resp.GetMessages()[0]
	if !got.GetSkipUnread() || !bytes.Equal(got.GetMeta(), []byte(forwardMeta)) {
		return fmt.Errorf("forward seq %d has skip_unread %v meta %q, want true and %q", seq, got.GetSkipUnread(), got.GetMeta(), forwardMeta)
	}
	if err := e2e.CheckForward(got, e2e.Origin{Room: src, Seq: 1, Author: r.peer, Text: e2e.TextFor(roomsCID + "r1-1")}); err != nil {
		return err
	}
	return r.awaitRooms("unread of room "+room+" after the forward", []string{room}, func(resp *chatimv1.GetRoomsResponse) error {
		return e2e.CheckLine(resp.GetItems(), r.text(room, 2, seq, r.peer))
	})
}
