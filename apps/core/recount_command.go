package main

import (
	"context"
	"errors"
	cliflag "flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

var (
	errRecountUsage = errors.New("usage: core recount -room ID [-dry-run]")
	errRecountRaced = errors.New("member count changed while recounting, run recount again")
)

type recountOptions struct {
	Room   uint64
	DryRun bool
}

type recountRooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	CountMembers(ctx context.Context, room uint64) (int, error)
	SetMemberCount(ctx context.Context, room, base uint64, count int) (domain.MemberCount, bool, error)
}

type recounter struct {
	rooms recountRooms
	pub   resync.Publisher
	root  string
	now   func() time.Time
	out   io.Writer
}

func parseRecountArgs(args []string, stderr io.Writer) (recountOptions, error) {
	fs := cliflag.NewFlagSet("recount", cliflag.ContinueOnError)
	fs.SetOutput(stderr)
	var o recountOptions
	var room string
	fs.StringVar(&room, "room", "", "room id to recount")
	fs.BoolVar(&o.DryRun, "dry-run", false, "print the stored and counted numbers without writing")
	if err := fs.Parse(args); err != nil {
		return recountOptions{}, fmt.Errorf("%w: %w", errRecountUsage, err)
	}
	if fs.NArg() > 0 {
		return recountOptions{}, fmt.Errorf("%w: unexpected arguments %v", errRecountUsage, fs.Args())
	}
	id, err := ids.ParseRoomID(room)
	if err != nil {
		return recountOptions{}, fmt.Errorf("%w: -room: %w", errRecountUsage, err)
	}
	o.Room = id
	return o, nil
}

func recountMain(args []string) int {
	opts, err := parseRecountArgs(args, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.ErrorContext(ctx, "invalid core config", "err", err)
		return 1
	}
	log := redactedLogger(logger, cfg)
	if err := runRecount(ctx, cfg, opts, log, os.Stdout); err != nil {
		log.ErrorContext(ctx, "recount failed", "err", err)
		return 1
	}
	return 0
}

func runRecount(ctx context.Context, cfg config.Config, opts recountOptions, log *slog.Logger, out io.Writer) error {
	c := &clients{}
	defer c.close(ctx, log)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	err := c.connectMongo(connectCtx, cfg)
	cancel()
	if err == nil {
		err = c.connectNATS(cfg, log)
	}
	if err != nil {
		return err
	}
	st := mongostore.New(c.mongo.Database(cfg.MongoDB), mongostore.Options{})
	r := recounter{rooms: st, pub: c.js, root: cfg.Stream.SubjectRoot, now: time.Now, out: out}
	return r.run(ctx, opts)
}

func (r recounter) run(ctx context.Context, opts recountOptions) error {
	room, err := r.rooms.Get(ctx, opts.Room)
	if err != nil {
		return err
	}
	n, err := r.rooms.CountMembers(ctx, room.ID)
	if err != nil {
		return fmt.Errorf("count members of room %d: %w", room.ID, err)
	}
	fmt.Fprintf(r.out, "room=%s stored=%d counted=%d\n", pbconv.RoomID(room.ID), room.MemberCount, n)
	if opts.DryRun {
		return nil
	}
	c, ok, err := r.rooms.SetMemberCount(ctx, room.ID, room.MemberCountVer, n)
	switch {
	case err != nil:
		return fmt.Errorf("set member count of room %d: %w", room.ID, err)
	case !ok:
		fmt.Fprintf(r.out, "member_count_ver moved past %d, nothing written\n", room.MemberCountVer)
		return fmt.Errorf("room %d: %w", room.ID, errRecountRaced)
	}
	msg, err := publish.Message(r.root, room.ID, pbconv.MemberCountChanged(room, c, "", r.now().UTC().Truncate(time.Millisecond)))
	if err != nil {
		return fmt.Errorf("build member_count_changed of room %d: %w", room.ID, err)
	}
	if _, err := r.pub.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("publish member_count_changed of room %d: %w", room.ID, err)
	}
	fmt.Fprintf(r.out, "member_count_ver=%d\n", c.Ver)
	return nil
}
