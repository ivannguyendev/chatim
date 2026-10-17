package main

import (
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func (r *mentionRun) edit(seq uint64, base uint32, set *chatimv1.MentionSet) (uint32, error) {
	req := &chatimv1.EditMessageRequest{RoomId: r.room, Seq: seq, BaseVer: base, Text: fmt.Sprintf("e2e mention edit %d", base+1), Mentions: set}
	resp, _, err := r.cl.EditMessage(r.as(r.sender), req)
	if err != nil {
		return 0, fmt.Errorf("edit seq %d from version %d: %w", seq, base, err)
	}
	if ver := resp.GetMessage().GetVer(); ver != base+1 {
		return 0, fmt.Errorf("edit seq %d from version %d gave version %d", seq, base, ver)
	}
	return base + 1, nil
}

func (r *mentionRun) minhSees(seq uint64, listed bool) error {
	at := e2e.Place{Room: r.room, Seq: seq}
	return r.poll("mentions of "+r.minh, func() (error, error) {
		items, err := r.mentionsOf(r.minh)
		if err != nil {
			return nil, err
		}
		switch found, read := e2e.MentionOf(items, at); {
		case found != listed:
			return fmt.Errorf("seq %d listed %v, want %v", seq, found, listed), nil
		case read:
			return nil, fmt.Errorf("seq %d is listed as read, want unread", seq)
		}
		return nil, nil
	})
}

func (r *mentionRun) editAddsMinh() error {
	seq, err := r.send(r.room, r.sender, mentionsCID+"edit", nil)
	if err != nil {
		return err
	}
	ver, err := r.edit(seq, 0, targets(userKind, r.minh))
	if err != nil {
		return err
	}
	if err := r.expectNotices(e2e.NotifiedBy(r.room, seq, r.minh, ver, e2e.UserTarget(r.minh))); err != nil {
		return err
	}
	r.edited = seq
	return r.minhSees(seq, true)
}

func (r *mentionRun) editDropsMinh() error {
	ver, err := r.edit(r.edited, 1, &chatimv1.MentionSet{})
	if err != nil {
		return err
	}
	if err := r.expectNotices(e2e.RetractedFor(r.room, r.edited, r.minh, ver, e2e.ReasonEdited)); err != nil {
		return err
	}
	return r.minhSees(r.edited, false)
}
