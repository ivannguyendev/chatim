package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const e2eUsage = "usage: corecli e2e setup|send|change|check [flags]"

func e2eCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(e2eUsage)
	}
	steps := map[string]command{"setup": e2eSetup, "send": e2eSend, "change": e2eChange, "check": e2eCheck}
	step, ok := steps[args[0]]
	if !ok {
		return errors.New(e2eUsage)
	}
	return step(ctx, args[1:])
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func e2eSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e setup", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	owner := fs.String("owner", "", "keep creating rooms until one lands on a slot this core owns")
	tries := fs.Int("tries", 64, "rooms to create at most while looking for -owner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		if *owner != "" && e2e.ShareOf(s.res.Slot).Owned[*owner] == 0 {
			return fmt.Errorf("core %q owns no slot", *owner)
		}
		for i := range *tries {
			room, rt, err := createRoomOnSlot(ctx, s, o.user)
			if err != nil {
				return err
			}
			if !rt.Owner || (*owner != "" && rt.Core != *owner) {
				continue
			}
			st := e2e.State{Tenant: o.tenant, User: o.user, Room: room, Owner: rt.Core}
			if err := e2e.Save(statePath(*dir), st); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "room %s lands on slot %d owned by %s (%d room(s) created)\n", room, slotOf(room), rt.Core, i+1)
			fmt.Fprintln(os.Stdout, room, rt.Core)
			return nil
		}
		return fmt.Errorf("no room landed on a slot owned by %q in %d tries", *owner, *tries)
	})
}

func createRoomOnSlot(ctx context.Context, s *session, user string) (string, slotmap.Route, error) {
	if err := s.res.Refresh(ctx); err != nil {
		return "", slotmap.Route{}, fmt.Errorf("load slot table: %w", err)
	}
	req := &chatimv1.CreateRoomRequest{
		Type:    chatimv1.RoomType_ROOM_TYPE_GROUP,
		Name:    "e2e " + time.Now().UTC().Format(time.RFC3339),
		Members: []string{user},
	}
	resp, _, err := s.client.CreateRoom(ctx, req)
	if err != nil {
		return "", slotmap.Route{}, fmt.Errorf("create room: %w", err)
	}
	room := resp.GetRoom().GetId()
	rt, _ := s.res.Slot(slotOf(room))
	return room, rt, nil
}

func slotOf(room string) uint16 {
	id, err := ids.ParseRoomID(room)
	if err != nil {
		return slotmap.Count
	}
	return slotmap.Of(id)
}
