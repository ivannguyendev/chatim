package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const listMyRoomsPath = "/chatim.v1.CoreService/ListMyRooms"

func sameEventually[M proto.Message](r *gatewayRun, what string, viaGateway, viaGRPC func() (M, error)) error {
	var last error
	for range gatewaySameAttempts {
		g, err := viaGateway()
		if err != nil {
			return fmt.Errorf("%s through the gateway: %w", what, err)
		}
		c, err := viaGRPC()
		if err != nil {
			return fmt.Errorf("%s through gRPC: %w", what, err)
		}
		if last = e2e.SameReply(what, g, c); last == nil {
			return nil
		}
		if !r.pause() {
			return r.base.Err()
		}
	}
	return last
}

func (r *gatewayRun) readsMatch() error {
	sent, _, err := r.cl.SendMessage(r.base, &chatimv1.SendMessageRequest{RoomId: r.room, Cid: randomCID(), Text: "e2e gateway message"})
	if err != nil {
		return fmt.Errorf("send through gRPC: %w", err)
	}
	r.seq = sent.GetSeq()
	if _, _, err := r.cl.SetBookmark(r.base, &chatimv1.SetBookmarkRequest{RoomId: r.room, Seq: r.seq, On: true}); err != nil {
		return fmt.Errorf("bookmark through gRPC: %w", err)
	}
	if _, err := r.b.await(0, e2e.WSMatch{Frame: e2e.FrameEvent, Kind: "message_created", ID: e2e.MessageEventID(r.room, r.seq)}); err != nil {
		return err
	}
	rooms := &chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Limit: 50}
	history := &chatimv1.GetHistoryRequest{RoomId: r.room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: wsHistoryLimit}
	members := &chatimv1.ListMembersRequest{RoomId: r.room, Limit: 50}
	marks := &chatimv1.ListBookmarksRequest{Limit: wsBookmarks}
	checks := []func() error{
		func() error {
			return sameEventually(r, "ListMyRooms",
				func() (*chatimv1.ListMyRoomsResponse, error) { return viaGateway(r.a, r.a.core.ListMyRooms, rooms) },
				func() (*chatimv1.ListMyRoomsResponse, error) {
					resp, _, err := r.cl.ListMyRooms(r.base, rooms)
					return resp, err
				})
		},
		func() error {
			return sameEventually(r, "GetHistory",
				func() (*chatimv1.GetHistoryResponse, error) { return viaGateway(r.a, r.a.core.GetHistory, history) },
				func() (*chatimv1.GetHistoryResponse, error) {
					resp, _, err := r.cl.GetHistory(r.base, history)
					return resp, err
				})
		},
		func() error {
			return sameEventually(r, "ListMembers",
				func() (*chatimv1.ListMembersResponse, error) { return viaGateway(r.a, r.a.core.ListMembers, members) },
				func() (*chatimv1.ListMembersResponse, error) {
					resp, _, err := r.cl.ListMembers(r.base, members)
					return resp, err
				})
		},
		func() error {
			return sameEventually(r, "ListBookmarks",
				func() (*chatimv1.ListBookmarksResponse, error) { return viaGateway(r.a, r.a.core.ListBookmarks, marks) },
				func() (*chatimv1.ListBookmarksResponse, error) {
					resp, _, err := r.cl.ListBookmarks(r.base, marks)
					return resp, err
				})
		},
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	r.notes = append(r.notes, fmt.Sprintf("four reads equal gRPC after seq %d", r.seq))
	return nil
}

func (r *gatewayRun) flushedRead() error {
	if _, err := r.b.call(markReadRequest(r.room, r.seq)); err != nil {
		return fmt.Errorf("peer B marks read and waits for its answer: %w", err)
	}
	list, err := viaGateway(r.b.gw, r.b.gw.core.ListMyRooms, &chatimv1.ListMyRoomsRequest{Folder: chatimv1.RoomFolder_ROOM_FOLDER_MAIN, Limit: 50})
	if err != nil {
		return fmt.Errorf("peer B lists rooms through the gateway: %w", err)
	}
	for _, item := range list.GetItems() {
		if item.GetRoom().GetId() == r.room {
			if got := item.GetMe().GetReadSeq(); got != r.seq {
				return fmt.Errorf("peer B's room list shows read_seq %d, want %d", got, r.seq)
			}
			return nil
		}
	}
	return fmt.Errorf("peer B's room list misses %s", r.room)
}

func (r *gatewayRun) do(method, path string, header http.Header) (int, http.Header, error) {
	ctx, cancel := context.WithTimeout(r.base, r.timeout)
	defer cancel()
	body := ""
	if method == http.MethodPost {
		body = "{}"
	}
	req, err := http.NewRequestWithContext(ctx, method, r.gatewayURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header = header
	resp, err := (&http.Client{Timeout: r.timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Header, nil
}

func (r *gatewayRun) expiredRefused() error {
	token, err := r.sign(r.aUser, time.Now().Add(-2*time.Hour), time.Hour)
	if err != nil {
		return err
	}
	code, _, err := r.do(http.MethodPost, listMyRoomsPath, http.Header{"Authorization": {"Bearer " + token}, "Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	if code != http.StatusUnauthorized {
		return fmt.Errorf("an expired token got status %d, want 401", code)
	}
	return nil
}

func (r *gatewayRun) sendRefused() error {
	_, err := viaGateway(r.a, r.a.core.SendMessage, &chatimv1.SendMessageRequest{RoomId: r.room, Cid: randomCID(), Text: "never sent"})
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		return fmt.Errorf("SendMessage through the gateway = %w, want unimplemented", err)
	}
	return nil
}

func (r *gatewayRun) preflight() error {
	code, h, err := r.do(http.MethodOptions, listMyRoomsPath, http.Header{
		"Origin": {r.origin}, "Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"authorization,content-type"},
	})
	if err != nil {
		return err
	}
	if code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != r.origin ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "Authorization") || h.Get("Access-Control-Allow-Credentials") != "" {
		return fmt.Errorf("preflight from %s = %d %v", r.origin, code, h)
	}
	return nil
}
