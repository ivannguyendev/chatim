package main

import (
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	roomFilesMedia = chatimv1.RoomFileKind_ROOM_FILE_KIND_MEDIA
	roomFilesOther = chatimv1.RoomFileKind_ROOM_FILE_KIND_FILE
)

func listRoomFiles(g *gateway, room string, kind chatimv1.RoomFileKind) (*chatimv1.ListRoomFilesResponse, error) {
	return viaGateway(g, g.media.ListRoomFiles, &chatimv1.ListRoomFilesRequest{RoomId: room, Kind: kind, PageSize: 10})
}

func (r *mediaRun) roomFiles() error {
	for _, tab := range []struct {
		kind chatimv1.RoomFileKind
		file *mediaFile
	}{{roomFilesMedia, r.image}, {roomFilesOther, r.page}} {
		resp, err := listRoomFiles(r.b.gw, r.room, tab.kind)
		if err != nil {
			return err
		}
		if err := r.oneRoomFile(resp, tab.file, r.seq); err != nil {
			return fmt.Errorf("%s tab: %w", tab.kind, err)
		}
		if err := inBucket(tab.file.name, resp.GetFiles()[0].GetUrl(), r.completedBucket); err != nil {
			return err
		}
	}
	c, err := r.client(r.third)
	if err != nil {
		return err
	}
	_, err = listRoomFiles(c, r.room, roomFilesMedia)
	return wantCode(err, connect.CodePermissionDenied)
}

func (r *mediaRun) oneRoomFile(resp *chatimv1.ListRoomFilesResponse, want *mediaFile, seq uint64) error {
	files := resp.GetFiles()
	if len(files) != 1 || resp.GetNextBefore() != "" || !resp.GetExpiresAt().IsValid() {
		return fmt.Errorf("listed %v", resp)
	}
	f := files[0]
	if f.GetMessage().GetSeq() != seq || f.GetSenderId() != r.user || !f.GetSentAt().IsValid() || f.GetUrl() == "" {
		return fmt.Errorf("room file %v", f)
	}
	return want.sameAttachment(f.GetFile())
}

func (r *mediaRun) roomFilesAfterDelete() error {
	for _, kind := range []chatimv1.RoomFileKind{roomFilesMedia, roomFilesOther} {
		resp, err := listRoomFiles(r.a.gw, r.room, kind)
		if err != nil {
			return err
		}
		if len(resp.GetFiles()) != 0 {
			return fmt.Errorf("the deleted message still lists %v", resp.GetFiles())
		}
	}
	c, err := r.client(r.third)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(r.wait)
	for {
		resp, err := listRoomFiles(c, r.forwardRoom, roomFilesMedia)
		if err != nil {
			return err
		}
		if len(resp.GetFiles()) == 1 {
			return r.oneRoomFile(resp, r.image, r.forwardSeq)
		}
		if len(resp.GetFiles()) > 1 || time.Now().After(deadline) || !backoff.Pause(r.base, time.Second) {
			return fmt.Errorf("the forward room lists %v", resp.GetFiles())
		}
	}
}
