package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const slotPoll = 500 * time.Millisecond

func slotsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("slots", flag.ContinueOnError)
	o := addOptions(fs)
	cores := fs.Int("cores", 0, "with -wait, the number of live cores that must share every slot evenly")
	wait := fs.Duration("wait", 0, "wait up to this long for -cores cores to share every slot evenly")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *wait > 0 && *cores <= 0 {
		return errors.New("-wait needs -cores")
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		sh, err := awaitShare(ctx, s, *cores, *wait)
		fmt.Fprintln(os.Stdout, sh)
		return err
	})
}

func awaitShare(ctx context.Context, s *session, cores int, wait time.Duration) (e2e.Share, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := s.res.Refresh(ctx); err != nil {
			return e2e.Share{}, fmt.Errorf("load slot table: %w", err)
		}
		sh := e2e.ShareOf(s.res.Slot)
		switch {
		case wait <= 0 || sh.Balanced(cores):
			return sh, nil
		case time.Now().After(deadline):
			return sh, fmt.Errorf("%d cores did not share every slot evenly within %v", cores, wait)
		case !backoff.Pause(ctx, slotPoll):
			return sh, ctx.Err()
		}
	}
}
