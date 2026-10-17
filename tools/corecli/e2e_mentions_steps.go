package main

import (
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	mentionsCID = "e2e-mentions-"
	userKind    = chatimv1.MentionKind_MENTION_KIND_USER
	groupKind   = chatimv1.MentionKind_MENTION_KIND_GROUP
)

func (r *mentionRun) setGroups() error {
	req := &chatimv1.SetGroupsRequest{Items: []*chatimv1.GroupItem{
		{Id: saleGroup, Name: saleName, Active: true, Ver: catalogVer},
		{Id: hrGroup, Name: hrName, Ver: catalogVer},
	}}
	resp, _, err := r.cl.SetGroups(r.as(r.sender), req)
	if err != nil {
		return fmt.Errorf("set groups: %w", err)
	}
	if changed := resp.GetChangedGroups(); len(changed) > 0 {
		if err := e2e.CheckChanged("set groups", changed, saleGroup, hrGroup); err != nil {
			return err
		}
		if err := r.awaitCatalog(); err != nil {
			return err
		}
	}
	created, _, err := r.cl.CreateRoom(r.as(r.sender), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e mentions " + r.st.Room,
		Members: []string{r.sender, r.an, r.binh, r.chi, r.minh}, RequestId: mentionsCID + r.st.Room,
	})
	if err != nil {
		return fmt.Errorf("create the mention room: %w", err)
	}
	r.room = created.GetRoom().GetId()
	return nil
}

func (r *mentionRun) setUserGroups() error {
	changed, err := r.setUserGroupsAs(
		userItem(r.sender, 1, saleGroup), userItem(r.an, 1, saleGroup), userItem(r.binh, 1, saleGroup), userItem(r.chi, 1, hrGroup),
	)
	if err == nil {
		err = e2e.CheckChanged("first user groups", changed, r.sender, r.an, r.binh, r.chi)
	}
	if err != nil {
		return err
	}
	if changed, err = r.setUserGroupsAs(userItem(r.binh, 2, hrGroup), userItem(r.chi, 2, saleGroup, hrGroup)); err != nil {
		return err
	}
	return e2e.CheckChanged("moved user groups", changed, r.binh, r.chi)
}

func (r *mentionRun) mentionSale() error {
	seq, err := r.sendMentions(mentionsCID+"sale", targets(groupKind, saleGroup))
	if err != nil {
		return err
	}
	r.sale = seq
	return r.expectNotices(
		e2e.NotifiedBy(r.room, seq, r.an, 0, e2e.GroupTarget(saleGroup)),
		e2e.NotifiedBy(r.room, seq, r.chi, 0, e2e.GroupTarget(saleGroup)),
	)
}

func (r *mentionRun) mentionInactive() error {
	hr, err := r.sendMentions(mentionsCID+"hr", targets(groupKind, hrGroup))
	if err != nil {
		return err
	}
	inactive := e2e.Place{Room: r.room, Seq: hr}
	err = r.poll("mentions of "+r.chi, func() (error, error) {
		items, err := r.mentionsOf(r.chi, saleGroup, hrGroup)
		if err != nil {
			return nil, err
		}
		if found, _ := e2e.MentionOf(items, inactive); found {
			return nil, fmt.Errorf("lists seq %d mentioning the inactive group %s", hr, hrGroup)
		}
		if found, _ := e2e.MentionOf(items, e2e.Place{Room: r.room, Seq: r.sale}); !found {
			return fmt.Errorf("seq %d mentioning %s is not listed", r.sale, saleGroup), nil
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	items, err := r.mentionsOf(r.binh, hrGroup)
	if found, _ := e2e.MentionOf(items, inactive); err == nil && found {
		err = fmt.Errorf("mentions of %s list seq %d mentioning the inactive group %s", r.binh, hr, hrGroup)
	}
	return err
}

func (r *mentionRun) deleteRetracts() error {
	resp, _, err := r.cl.DeleteMessage(r.as(r.sender), &chatimv1.DeleteMessageRequest{RoomId: r.room, Seq: r.sale})
	if err != nil {
		return fmt.Errorf("delete seq %d: %w", r.sale, err)
	}
	ver := resp.GetMessage().GetVer()
	return r.expectNotices(
		e2e.RetractedFor(r.room, r.sale, r.an, ver, e2e.ReasonDeleted),
		e2e.RetractedFor(r.room, r.sale, r.chi, ver, e2e.ReasonDeleted),
	)
}

func (r *mentionRun) mentionAll() error {
	seq, err := r.sendMentions(mentionsCID+"all", &chatimv1.MentionSet{All: true})
	if err != nil {
		return err
	}
	var ns []e2e.Notice
	for _, user := range []string{r.an, r.binh, r.chi, r.minh} {
		ns = append(ns, e2e.NotifiedBy(r.room, seq, user, 0, e2e.AllTarget))
	}
	return r.expectNotices(ns...)
}

func (r *mentionRun) roomGroups() error {
	resp, _, err := r.cl.ListRoomGroups(r.as(r.sender), &chatimv1.ListRoomGroupsRequest{RoomId: r.room})
	if err != nil {
		return fmt.Errorf("room groups of %s: %w", r.room, err)
	}
	if got, want := e2e.FormatRoomGroups(resp.GetGroups()), saleGroup+"="+saleName; got != want {
		return fmt.Errorf("room groups of %s are %q, want %q", r.room, got, want)
	}
	return nil
}

func (r *mentionRun) olderUserGroups() error {
	changed, err := r.setUserGroupsAs(userItem(r.binh, 1, saleGroup))
	if err != nil {
		return err
	}
	if len(changed) != 0 {
		return fmt.Errorf("an older user group list changed %v", changed)
	}
	seq, err := r.sendMentions(mentionsCID+"sale-again", targets(groupKind, saleGroup))
	if err != nil {
		return err
	}
	return r.expectNotices(
		e2e.NotifiedBy(r.room, seq, r.an, 0, e2e.GroupTarget(saleGroup)),
		e2e.NotifiedBy(r.room, seq, r.chi, 0, e2e.GroupTarget(saleGroup)),
	)
}
