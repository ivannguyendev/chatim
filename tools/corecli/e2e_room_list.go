package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	listRooms  = 4
	listPage   = 10
	sysMeta    = `{"event":"e2e"}`
	settlePoll = 500 * time.Millisecond
)

var errRoomsTwice = errors.New("this scenario already ran its room list phase")

type roomRun struct {
	*linkRun
	me   string
	bot  string
	list [listRooms]string
	sys  uint64
}

func e2eRooms(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e rooms", flag.ContinueOnError)
	o := addOptions(fs)
	live := addLiveFlags(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for the live events and room activity of each step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if st.RoomRun {
		return errRoomsTwice
	}
	if err := singleTokens(st.Tenant, *live.root); err != nil {
		return err
	}
	feed, err := openLive(*live.url, *live.root+"."+st.Tenant+".*.*.>")
	if err != nil {
		return err
	}
	defer feed.Close()
	r := &roomRun{
		linkRun: &linkRun{memberRun: &memberRun{st: &st, live: feed, root: *live.root, wait: *wait}, peer: "e2e-leo-" + st.Room},
		me:      "e2e-mia-" + st.Room,
		bot:     "e2e-bot-" + st.Room,
	}
	err = withRoutes(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	st.RoomRun = true
	fmt.Fprintf(os.Stderr, "room list phase ok on rooms %v: %d live events checked by id, kind, subject and payload\n", r.list, len(r.wants))
	return e2e.Save(statePath(*dir), st)
}

func (r *roomRun) run() error {
	steps := []func() error{
		r.createRooms, r.unreadFromPeer, r.systemMessages, r.readDeleteHide, r.pinMovesFirst, r.archiveMovesFolder,
		r.secondPage, r.getRoomsRefreshes, r.updateRoom, r.aroundAndMessages, r.threadAfterTwoReplies,
		r.reactorsListed, r.pinsNewestFirst, r.membersOwnerFirst, r.forwardKeepsExtras,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("room list step %d: %w", i+1, err)
		}
		fmt.Fprintf(os.Stderr, "room list step %d ok\n", i+1)
	}
	return nil
}

func (r *roomRun) createRooms() error {
	var wants []e2e.Want
	for i := range r.list {
		req := &chatimv1.CreateRoomRequest{
			Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "e2e rooms " + strconv.Itoa(i+1), Members: []string{r.me, r.peer},
			RequestId: "e2e-rooms-" + strconv.Itoa(i+1) + "-" + r.st.Room,
		}
		resp, _, err := r.cl.CreateRoom(r.as(r.me), req)
		if err != nil {
			return fmt.Errorf("create room %d: %w", i+1, err)
		}
		r.list[i] = resp.GetRoom().GetId()
		r.watch(r.list[i])
		wants = append(wants, r.founded(r.list[i], r.me)...)
	}
	return r.expect(wants...)
}

func (r *roomRun) myRooms(folder chatimv1.RoomFolder, next string, limit uint32) (*chatimv1.ListMyRoomsResponse, error) {
	resp, _, err := r.cl.ListMyRooms(r.as(r.me), &chatimv1.ListMyRoomsRequest{Folder: folder, Next: next, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("rooms of %s in %s: %w", r.me, folder, err)
	}
	return resp, nil
}

func (r *roomRun) settle(what string, check func() (pending, err error)) error {
	ctx, cancel := context.WithTimeout(r.base, r.wait)
	defer cancel()
	began := time.Now()
	for {
		pending, err := check()
		switch {
		case err != nil:
			return fmt.Errorf("%s: %w", what, err)
		case pending == nil:
			fmt.Fprintf(os.Stderr, "room list settled after %dms: %s\n", time.Since(began).Milliseconds(), what)
			return nil
		case !backoff.Pause(ctx, settlePoll):
			return fmt.Errorf("%s after %v: %w", what, r.wait, pending)
		}
	}
}

func (r *roomRun) awaitList(what string, folder chatimv1.RoomFolder, check func([]*chatimv1.RoomItem) error) error {
	return r.settle(what, func() (error, error) {
		resp, err := r.myRooms(folder, "", listPage)
		if err != nil {
			return nil, err
		}
		return check(resp.GetItems()), nil
	})
}

func (r *roomRun) awaitRooms(what string, rooms []string, check func(*chatimv1.GetRoomsResponse) error) error {
	return r.settle(what, func() (error, error) {
		resp, _, err := r.cl.GetRooms(r.as(r.me), &chatimv1.GetRoomsRequest{RoomIds: rooms})
		if err != nil {
			return nil, fmt.Errorf("get rooms %v: %w", rooms, err)
		}
		return check(resp), nil
	})
}

func (r *roomRun) first(what string, want e2e.RoomLine) error {
	return r.awaitList(what, chatimv1.RoomFolder_ROOM_FOLDER_MAIN, func(items []*chatimv1.RoomItem) error { return e2e.CheckFirst(items, want) })
}

func (r *roomRun) line(what string, want e2e.RoomLine) error {
	return r.awaitList(what, chatimv1.RoomFolder_ROOM_FOLDER_MAIN, func(items []*chatimv1.RoomItem) error { return e2e.CheckLine(items, want) })
}

func (r *roomRun) text(room string, unread uint32, preview uint64, sender string) e2e.RoomLine {
	return e2e.RoomLine{Room: room, Unread: unread, Preview: preview, Sender: sender, Kind: "text"}
}
