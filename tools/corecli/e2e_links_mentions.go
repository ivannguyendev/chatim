package main

import (
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const outsideCID = "e2e-link-outside-"

func (r *linkRun) mentionsNeedMembership() error {
	created, _, err := r.cl.CreateRoom(r.as(r.st.User), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e outside " + r.st.Room, Members: []string{r.st.User}, RequestId: "e2e-links-outside-" + r.st.Room,
	})
	if err != nil {
		return fmt.Errorf("create a group without %s: %w", r.peer, err)
	}
	outside := created.GetRoom().GetId()
	before := &chatimv1.MentionSet{Targets: []*chatimv1.MentionTarget{{Kind: chatimv1.MentionKind_MENTION_KIND_GROUP, Id: r.team}}, All: true}
	if _, err := r.send(outside, r.st.User, outsideCID+"1", func(req *chatimv1.SendMessageRequest) { req.Mentions = before }); err != nil {
		return err
	}
	add := &chatimv1.AddMembersRequest{RoomId: outside, Users: []string{r.peer}, RequestId: "e2e-links-outside-add-" + r.st.Room}
	if _, _, err := r.cl.AddMembers(r.as(r.st.User), add); err != nil {
		return fmt.Errorf("add %s to room %s: %w", r.peer, outside, err)
	}
	after, err := r.send(outside, r.st.User, outsideCID+"2", func(req *chatimv1.SendMessageRequest) { req.Mentions = &chatimv1.MentionSet{All: true} })
	if err != nil {
		return err
	}
	want := append([]e2e.Place{{Room: outside, Seq: after}}, e2e.PlacesIn(r.group, r.mentioned)...)
	req := &chatimv1.ListMentionsRequest{Groups: []*chatimv1.MentionGroup{{Id: r.team}}}
	return r.poll("mentions of "+r.peer+" after joining room "+outside, func() (error, error) {
		resp, _, err := r.cl.ListMentions(r.as(r.peer), req)
		if err != nil {
			return nil, err
		}
		return e2e.CheckMentions("mentions of "+r.peer, resp.GetItems(), want), nil
	})
}
