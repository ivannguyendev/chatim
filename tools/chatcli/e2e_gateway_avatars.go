package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	avatarFirstRead = 4 * time.Second
	avatarWait      = 75 * time.Second
)

func (r *gatewayRun) avatarUsers() ([]string, error) {
	resp, err := viaGateway(r.a, r.a.chat.GetRooms, &chatimv1.GetRoomsRequest{RoomIds: []string{r.room}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetItems()) != 1 {
		return nil, fmt.Errorf("GetRooms gave %d items for %s", len(resp.GetItems()), r.room)
	}
	return resp.GetItems()[0].GetRoom().GetAvatarUsers(), nil
}

func (r *gatewayRun) awaitAvatars(done func([]string) bool) ([]string, error) {
	if !backoff.Pause(r.base, avatarFirstRead) {
		return nil, r.base.Err()
	}
	deadline := time.Now().Add(avatarWait)
	for {
		got, err := r.avatarUsers()
		if err != nil {
			return nil, err
		}
		if done(got) {
			return got, nil
		}
		if time.Now().After(deadline) || !backoff.Pause(r.base, time.Second) {
			return nil, fmt.Errorf("avatar users of %s stayed %v for %s", r.room, got, avatarWait)
		}
	}
}

func sameSet(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

func (r *gatewayRun) avatarsOfThree() error {
	want := []string{r.aUser, r.b.user, r.cUser}
	_, err := r.awaitAvatars(func(got []string) bool { return sameSet(got, want) })
	return err
}

func (r *gatewayRun) avatarsFollowRemoval() error {
	d, e := r.cUser+"-d", r.cUser+"-e"
	if _, err := viaGateway(r.a, r.a.chat.AddMembers, &chatimv1.AddMembersRequest{RoomId: r.room, Users: []string{d, e}, RequestId: randomCID()}); err != nil {
		return fmt.Errorf("add %s and %s: %w", d, e, err)
	}
	all := []string{r.aUser, r.b.user, r.cUser, d, e}
	four, err := r.awaitAvatars(func(got []string) bool {
		return len(got) == 4 && !slices.ContainsFunc(got, func(u string) bool { return !slices.Contains(all, u) })
	})
	if err != nil {
		return err
	}
	victim := ""
	for _, u := range []string{r.cUser, d, e} {
		if slices.Contains(four, u) {
			victim = u
			break
		}
	}
	if victim == "" {
		return fmt.Errorf("avatar users %v hold none of C, D and E", four)
	}
	if _, err := viaGateway(r.a, r.a.chat.RemoveMember, &chatimv1.RemoveMemberRequest{RoomId: r.room, User: victim}); err != nil {
		return fmt.Errorf("remove %s: %w", victim, err)
	}
	rest := slices.DeleteFunc(slices.Clone(all), func(u string) bool { return u == victim })
	_, err = r.awaitAvatars(func(got []string) bool { return sameSet(got, rest) })
	if err == nil {
		r.notes = append(r.notes, "avatar users followed adds and the removal of "+victim)
	}
	return err
}
