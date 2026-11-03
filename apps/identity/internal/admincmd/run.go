package admincmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/signing"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
)

const actor = "admin-cli"

var (
	errUsage            = errors.New("usage error")
	errSchemaIncomplete = errors.New("schema incomplete, run migrate -apply")
)

type Store interface {
	store.Admin
	store.TrustSource
	Migrate(ctx context.Context, apply bool) ([]string, error)
	Schema(ctx context.Context) (store.SchemaState, error)
	Ping(ctx context.Context) error
	Close()
}

type Env struct {
	Open         func(ctx context.Context) (Store, error)
	Sealer       *signing.Sealer
	Random       io.Reader
	Now          func() time.Time
	KeyWindow    time.Duration
	ActivateLead time.Duration
	Issuer       string
	Out          io.Writer
	Log          *slog.Logger
}

type command func(ctx context.Context, env Env, st Store, args []string) error

var commands = map[string]command{
	"migrate":           migrate,
	"tenant-create":     tenantCreate,
	"tenant-disable":    tenantState(store.StateDisabled),
	"tenant-enable":     tenantState(store.StateActive),
	"user-disable":      userState(store.StateDisabled),
	"user-enable":       userState(store.StateActive),
	"key-rotate":        keyRotate,
	"key-activate":      keyActivate,
	"jwks-export":       jwksExport,
	"bootstrap":         bootstrap,
	"group-create":      groupCreate,
	"group-update":      groupUpdate,
	"group-delete":      groupDelete,
	"user-groups":       userGroups,
	"user-set":          userSet,
	"issuer-add":        issuerAdd,
	"issuer-disable":    issuerState(store.StateDisabled),
	"issuer-enable":     issuerState(store.StateActive),
	"issuer-key-add":    issuerKeyAdd,
	"issuer-key-remove": issuerKeyRemove,
	"issuer-keys":       issuerKeys,
}

func Names() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Is(name string) bool { return slices.Contains(Names(), name) }

func Run(ctx context.Context, env Env, args []string) int {
	if len(args) == 0 || commands[args[0]] == nil {
		env.Log.ErrorContext(ctx, "unknown identity command", "commands", Names())
		return 2
	}
	st, err := env.Open(ctx)
	if err != nil {
		env.Log.ErrorContext(ctx, "open identity store", "err", err)
		return 1
	}
	defer st.Close()
	if err := st.Ping(ctx); err != nil {
		env.Log.ErrorContext(ctx, "reach identity store", "err", err)
		return 1
	}
	if err := runCommand(ctx, env, st, args); err != nil {
		if errors.Is(err, errUsage) {
			env.Log.ErrorContext(ctx, "identity command usage", "command", args[0], "err", err)
			return 2
		}
		env.Log.ErrorContext(ctx, "identity command failed", "command", args[0], "err", err)
		return 1
	}
	return 0
}

func runCommand(ctx context.Context, env Env, st Store, args []string) error {
	if !schemaFree[args[0]] {
		if err := requireSchema(ctx, st); err != nil {
			return err
		}
	}
	return commands[args[0]](ctx, env, st, args[1:])
}

var schemaFree = map[string]bool{"migrate": true, "bootstrap": true}

func requireSchema(ctx context.Context, st Store) error {
	state, err := st.Schema(ctx)
	if err != nil {
		return err
	}
	if len(state.Missing) > 0 {
		return fmt.Errorf("%w: %s", errSchemaIncomplete, strings.Join(state.Missing, "; "))
	}
	return nil
}

type flags struct {
	set *flag.FlagSet
}

func newFlags(name string) flags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return flags{set: fs}
}

func (f flags) parse(args []string, required ...string) error {
	if err := f.set.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, f.set.Args())
	}
	for _, name := range required {
		if f.set.Lookup(name).Value.String() == "" {
			return fmt.Errorf("%w: -%s is required", errUsage, name)
		}
	}
	return nil
}

func (f flags) given() map[string]bool {
	set := map[string]bool{}
	f.set.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	return set
}

func revocation(env Env, reason string) store.Revocation {
	return store.Revocation{By: actor, Reason: reason, At: env.Now()}
}
