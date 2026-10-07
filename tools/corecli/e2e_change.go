package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var errChangedTwice = errors.New("this scenario already holds its edit and delete")

func e2eChange(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e change", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	edit := fs.Uint64("edit", 1, "acked seq to edit from version 0")
	del := fs.Uint64("delete", 2, "acked seq to delete from version 0")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	acked := func(seq uint64) bool {
		return slices.ContainsFunc(st.Acks, func(a e2e.Ack) bool { return a.Seq == seq })
	}
	switch {
	case len(st.Changes) > 0:
		return errChangedTwice
	case *edit == *del || !acked(*edit) || !acked(*del):
		return fmt.Errorf("-edit %d and -delete %d must be two distinct acked seq", *edit, *del)
	}
	o.tenant, o.user = st.Tenant, st.User
	changes := []e2e.Change{e2e.EditOf(*edit), e2e.DeleteOf(*del)}
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		for _, c := range changes {
			if err := applyChange(ctx, s.client, st.Room, c); err != nil {
				return err
			}
			if err := checkVersions(ctx, s.client, st, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	st.Changes = changes
	fmt.Fprintf(os.Stderr, "edited seq %d and deleted seq %d at version 1; edit history ok\n", *edit, *del)
	return e2e.Save(statePath(*dir), st)
}

func applyChange(ctx context.Context, cl *route.Client, room string, c e2e.Change) error {
	var m *chatimv1.Message
	var stats route.Stats
	var err error
	if c.Deleted {
		var resp *chatimv1.DeleteMessageResponse
		resp, stats, err = cl.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: c.Seq})
		m = resp.GetMessage()
	} else {
		var resp *chatimv1.EditMessageResponse
		resp, stats, err = cl.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: c.Seq, Text: c.Text})
		m = resp.GetMessage()
	}
	if err != nil {
		return fmt.Errorf("%s seq %d: %w", c.Kind(), c.Seq, err)
	}
	if m.GetVer() != c.Version || m.GetText() != c.Text || m.GetDeleted() != c.Deleted || m.GetEditedAt() == nil {
		return fmt.Errorf("%s seq %d returned version %d text %q deleted %v, want version %d text %q deleted %v",
			c.Kind(), c.Seq, m.GetVer(), m.GetText(), m.GetDeleted(), c.Version, c.Text, c.Deleted)
	}
	report(c.Kind()+" seq "+strconv.FormatUint(c.Seq, 10), stats)
	return nil
}

func checkVersions(ctx context.Context, cl *route.Client, st e2e.State, c e2e.Change) error {
	resp, _, err := cl.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: st.Room, Seq: c.Seq, Limit: maxEditPage})
	if err != nil {
		return fmt.Errorf("edit history of seq %d: %w", c.Seq, err)
	}
	return e2e.CheckVersions(st.Acks, c, resp.GetVersions(), st.User)
}
