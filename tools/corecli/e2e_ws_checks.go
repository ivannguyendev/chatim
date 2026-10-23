package main

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	wsSentText   = "e2e websocket message"
	wsEditedText = "e2e websocket message, edited"
	wsBookmarks  = 10
)

func (r *wsRun) edit() error {
	resp, err := r.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_EditMessage{EditMessage: &chatimv1.EditMessageRequest{
		RoomId: r.room, Seq: r.seq, BaseVer: 0, Text: wsEditedText,
	}}})
	if err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	m := resp.GetEditMessage().GetMessage()
	if m.GetVer() != 1 || m.GetText() != wsEditedText {
		return fmt.Errorf("edit returned version %d text %q, want version 1 text %q", m.GetVer(), m.GetText(), wsEditedText)
	}
	return nil
}

func (r *wsRun) react() error {
	settings, err := viaGateway(r.gw, r.gw.core.GetReactionSettings, &chatimv1.GetReactionSettingsRequest{})
	if err != nil {
		return fmt.Errorf("reaction settings: %w", err)
	}
	if r.emoji, err = firstEmoji(settings.GetEmojis()); err != nil {
		return err
	}
	resp, err := r.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_ReactMessage{ReactMessage: &chatimv1.ReactMessageRequest{
		RoomId: r.room, Seq: r.seq, Emoji: r.emoji,
	}}})
	if err != nil {
		return fmt.Errorf("react: %w", err)
	}
	got := resp.GetReactMessage()
	return e2e.CheckReactReply(e2e.Reaction{Seq: r.seq, Emoji: r.emoji, Change: 1, Version: 1}, got.GetChange(), got.GetReactions())
}

func (r *wsRun) bookmark() error {
	resp, err := r.call(&chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_SetBookmark{SetBookmark: &chatimv1.SetBookmarkRequest{
		RoomId: r.room, Seq: r.seq, On: true,
	}}})
	if err != nil {
		return fmt.Errorf("bookmark: %w", err)
	}
	if !resp.GetSetBookmark().GetChanged() {
		return errors.New("bookmark returned changed false, want true")
	}
	list, err := r.bookmarks()
	if err != nil {
		return err
	}
	if !e2e.Bookmarked(list.GetItems(), r.room, r.seq) {
		return fmt.Errorf("bookmarks through the gateway miss %s seq %d", r.room, r.seq)
	}
	return nil
}

func (r *wsRun) bookmarks() (*chatimv1.ListBookmarksResponse, error) {
	resp, err := viaGateway(r.gw, r.gw.core.ListBookmarks, &chatimv1.ListBookmarksRequest{Limit: wsBookmarks})
	if err != nil {
		return nil, fmt.Errorf("bookmarks through the gateway: %w", err)
	}
	return resp, nil
}

func (r *wsRun) matchGRPC() error {
	viaWS, err := r.history()
	if err != nil {
		return err
	}
	want := e2e.WSMessage{Seq: r.seq, Sender: r.user, Text: wsEditedText, Ver: 1, Emoji: r.emoji, Bookmarked: true}
	if err := e2e.CheckWSMessage(e2e.MessageAt(viaWS.GetMessages(), r.seq), want); err != nil {
		return err
	}
	viaGRPC, _, err := r.cl.GetHistory(r.base, r.historyRequest())
	if err != nil {
		return fmt.Errorf("history through gRPC: %w", err)
	}
	if err := e2e.SameReply("history", viaWS, viaGRPC); err != nil {
		return err
	}
	marksWS, err := r.bookmarks()
	if err != nil {
		return err
	}
	marksGRPC, _, err := r.cl.ListBookmarks(r.base, &chatimv1.ListBookmarksRequest{Limit: wsBookmarks})
	if err != nil {
		return fmt.Errorf("bookmarks through gRPC: %w", err)
	}
	return e2e.SameReply("bookmarks", marksWS, marksGRPC)
}

func (r *wsRun) systemRefused() error {
	_, err := r.call(sendRequest(&chatimv1.SendMessageRequest{
		RoomId: r.room, Cid: randomCID(), Text: wsSentText, Kind: chatimv1.MessageKind_MESSAGE_KIND_SYSTEM,
	}))
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("send kind system = %w, want InvalidArgument", err)
	}
	return nil
}

func (r *wsRun) expiredRefused() error {
	token, err := r.sign(r.key, time.Now().Add(-2*time.Hour), time.Hour)
	if err != nil {
		return err
	}
	return r.refused("an expired token", token)
}
