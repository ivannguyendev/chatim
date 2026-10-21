package app

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

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/mongoindex"
	"github.com/ivannguyendev/chatim/pkg/redact"
)

var errIndexesUsage = errors.New("usage: core indexes [-dry-run|-apply|-drop-old]")

type IndexesMode int

const (
	IndexesDryRun IndexesMode = iota
	IndexesApply
	IndexesDropOld
)

type IndexesOptions struct {
	Mode IndexesMode
}

func parseIndexesArgs(args []string, stderr io.Writer) (IndexesOptions, error) {
	fs := cliflag.NewFlagSet("indexes", cliflag.ContinueOnError)
	fs.SetOutput(stderr)
	var dryRun, apply, dropOld bool
	fs.BoolVar(&dryRun, "dry-run", false, "print missing, outdated and unknown indexes without writing (default)")
	fs.BoolVar(&apply, "apply", false, "create missing indexes next to the old ones")
	fs.BoolVar(&dropOld, "drop-old", false, "drop outdated indexes whose replacement is built")
	if err := fs.Parse(args); err != nil {
		return IndexesOptions{}, fmt.Errorf("%w: %w", errIndexesUsage, err)
	}
	if fs.NArg() > 0 {
		return IndexesOptions{}, fmt.Errorf("%w: unexpected arguments %v", errIndexesUsage, fs.Args())
	}
	switch {
	case btoi(dryRun)+btoi(apply)+btoi(dropOld) > 1:
		return IndexesOptions{}, fmt.Errorf("%w: pick one mode", errIndexesUsage)
	case apply:
		return IndexesOptions{Mode: IndexesApply}, nil
	case dropOld:
		return IndexesOptions{Mode: IndexesDropOld}, nil
	default:
		return IndexesOptions{Mode: IndexesDryRun}, nil
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func indexesMain(args []string) int {
	opts, err := parseIndexesArgs(args, os.Stderr)
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
	if err := RunIndexes(ctx, cfg, opts, log, os.Stdout); err != nil {
		log.ErrorContext(ctx, "indexes failed", "err", err)
		return 1
	}
	return 0
}

func RunIndexes(ctx context.Context, cfg config.Config, opts IndexesOptions, log *slog.Logger, out io.Writer) error {
	c := &clients{}
	defer c.close(ctx, log)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	err := c.connectMongo(connectCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	db := c.mongo.Database(cfg.MongoDB)
	if err := runIndexes(ctx, db, indexCatalog(cfg), opts, out); err != nil {
		return redact.Error(err, cfg.MongoURI)
	}
	return nil
}

func runIndexes(ctx context.Context, db *mongo.Database, cat mongoindex.Catalog, opts IndexesOptions, out io.Writer) error {
	switch opts.Mode {
	case IndexesApply:
		created, err := mongoindex.Apply(ctx, db, cat)
		printDone(out, "created", created, noneUnless(err, "no missing index"))
		return err
	case IndexesDropOld:
		dropped, err := mongoindex.DropOld(ctx, db, cat)
		printDone(out, "dropped", dropped, noneUnless(err, "no outdated index"))
		return err
	default:
		reports, err := mongoindex.Inspect(ctx, db, cat)
		if err != nil {
			return err
		}
		var lines []string
		for _, r := range reports {
			lines = append(lines, r.Lines()...)
		}
		printDone(out, "", lines, "indexes up to date")
		return nil
	}
}

func noneUnless(err error, none string) string {
	if err != nil {
		return ""
	}
	return none
}

func printDone(out io.Writer, verb string, items []string, none string) {
	if len(items) == 0 {
		if none != "" {
			fmt.Fprintln(out, none)
		}
		return
	}
	for _, item := range items {
		if verb == "" {
			fmt.Fprintln(out, item)
			continue
		}
		fmt.Fprintln(out, verb, item)
	}
}
