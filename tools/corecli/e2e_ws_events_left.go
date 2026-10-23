package main

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/callerid"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const removedMemberVer = 2

func (r *wsEventsRun) leftListed() error {
	ctx := callerid.WithCaller(r.base, r.tenant, r.b.user)
	resp, _, err := r.cl.ListMyRoomIDs(ctx, &chatimv1.ListMyRoomIDsRequest{})
	if err != nil {
		return fmt.Errorf("list room ids of %s: %w", r.b.user, err)
	}
	if err := e2e.CheckLeftRoom(resp, r.room, removedMemberVer); err != nil {
		return err
	}
	r.notes = append(r.notes, fmt.Sprintf("B's left rooms hold the group at ver %d", removedMemberVer))
	return nil
}

func (r *wsEventsRun) reconnectB() error {
	r.b.close()
	token, err := devSign(r.key, r.tenant, r.b.user, time.Now(), devTokenTTL)
	if err != nil {
		return err
	}
	r.tokens = append(r.tokens, token)
	if err := dialPeer(r.base, r.url, token, r.b); err != nil {
		return err
	}
	seq, err := r.send("e2e websocket events after B reconnected", nil)
	if err != nil {
		return err
	}
	if _, err := r.a.await(0, r.created(seq)); err != nil {
		return err
	}
	if err := r.settle(); err != nil {
		return err
	}
	return r.b.absent(0, e2e.WSMatch{Room: r.room})
}
