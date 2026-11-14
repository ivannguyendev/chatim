package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/pkg/core/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (r *mediaRun) publicPageRefused() error {
	g, err := r.client(r.user)
	if err != nil {
		return err
	}
	_, err = viaGateway(g, g.media.StartUpload, &chatimv1.StartUploadRequest{FileName: "x.html", ContentType: "text/html", Size: 10, Public: true})
	return wantCode(err, connect.CodeInvalidArgument)
}

func (r *mediaRun) uploadAvatar() error {
	pic, err := pngBody()
	if err != nil {
		return err
	}
	r.avatar = newMediaFile(r.user, "group.png", "image/png", pic, []int32{mediaImageWidth, mediaImageHeight})
	r.avatar.public = true
	if err := r.uploadSmall(r.avatar); err != nil {
		return err
	}
	if !strings.HasPrefix(r.avatar.key, r.tenant+"/"+r.user+"/pub/") {
		return fmt.Errorf("public key %q has no pub segment", r.avatar.key)
	}
	return nil
}

func (r *mediaRun) publicInMessageRefused() error {
	_, err := r.a.call(sendRequest(&chatimv1.SendMessageRequest{RoomId: r.room, Cid: randomCID(), Attachments: []*chatimv1.Attachment{r.avatar.attachment()}}))
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("send with a public key = %w, want InvalidArgument", err)
	}
	return nil
}

func (r *mediaRun) updateAvatars(keys ...string) error {
	_, err := viaGateway(r.a.gw, r.a.gw.chat.UpdateRoom, &chatimv1.UpdateRoomRequest{RoomId: r.room, Avatars: &chatimv1.RoomAvatars{Keys: keys}})
	return err
}

func (r *mediaRun) privateAvatarRefused() error {
	return wantCode(r.updateAvatars(r.image.key), connect.CodeInvalidArgument)
}

func (r *mediaRun) roomAvatars() ([]string, error) {
	resp, err := viaGateway(r.b.gw, r.b.gw.chat.GetRooms, &chatimv1.GetRoomsRequest{RoomIds: []string{r.room}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetItems()) != 1 {
		return nil, fmt.Errorf("GetRooms gave %d items for %s", len(resp.GetItems()), r.room)
	}
	return resp.GetItems()[0].GetRoom().GetAvatars(), nil
}

func (r *mediaRun) setAvatar() error {
	if err := r.updateAvatars(r.avatar.key); err != nil {
		return err
	}
	got, err := r.roomAvatars()
	if err != nil || !slices.Equal(got, []string{r.avatar.key}) {
		return fmt.Errorf("avatars seen by B %v, want %s: %w", got, r.avatar.key, err)
	}
	return nil
}

func (r *mediaRun) avatarURL() (*chatimv1.FileURL, error) {
	resp, err := viaGateway(r.b.gw, r.b.gw.media.GetPublicURLs, &chatimv1.GetPublicURLsRequest{Keys: []string{r.avatar.key, r.image.key}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetFiles()) != 1 || resp.GetFiles()[0].GetKey() != r.avatar.key || !resp.GetExpiresAt().IsValid() {
		return nil, fmt.Errorf("GetPublicURLs gave %v, want only %s", resp.GetFiles(), r.avatar.key)
	}
	return resp.GetFiles()[0], nil
}

func (r *mediaRun) publicURLs() error {
	u, err := r.avatarURL()
	if err != nil {
		return err
	}
	if err := checkDownload(r.base, r.avatar, u.GetUrl()); err != nil {
		return err
	}
	a, err := r.client(r.user)
	if err != nil {
		return err
	}
	_, err = r.uploadKeys(a, nil, r.image.key, r.page.key, r.upload.GetKey())
	return err
}

func (r *mediaRun) avatarMoved() error {
	deadline := time.Now().Add(mediaMoveWait)
	for {
		u, err := r.avatarURL()
		if err != nil {
			return err
		}
		moved := inBucket(r.avatar.name, u.GetUrl(), r.completedBucket)
		if moved == nil {
			return checkDownload(r.base, r.avatar, u.GetUrl())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("avatar not moved after %s: %w", mediaMoveWait, moved)
		}
		if !backoff.Pause(r.base, mediaMovePoll) {
			return r.base.Err()
		}
	}
}

func (r *mediaRun) clearAvatar() error {
	if err := r.updateAvatars(); err != nil {
		return err
	}
	got, err := r.roomAvatars()
	if err != nil || len(got) != 0 {
		return fmt.Errorf("avatars seen by B after the clear %v: %w", got, err)
	}
	return nil
}
