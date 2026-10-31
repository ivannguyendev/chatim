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
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const actor = "admin-cli"

var errUsage = errors.New("usage error")

type Store interface {
	store.Admin
	Migrate(ctx context.Context, apply bool) ([]string, error)
	Ping(ctx context.Context) error
	Close()
}

type Env struct {
	Open         func(ctx context.Context) (Store, error)
	Sealer       *signing.Sealer
	Hasher       *password.Hasher
	Random       io.Reader
	Now          func() time.Time
	KeyWindow    time.Duration
	ActivateLead time.Duration
	Out          io.Writer
	Log          *slog.Logger
}

type command func(ctx context.Context, env Env, st Store, args []string) error

var commands = map[string]command{
	"migrate":         migrate,
	"tenant-create":   tenantCreate,
	"tenant-disable":  tenantState(store.StateDisabled),
	"tenant-enable":   tenantState(store.StateActive),
	"account-create":  accountCreate,
	"password-reset":  passwordReset,
	"account-disable": accountState(store.StateDisabled),
	"account-enable":  accountState(store.StateActive),
	"session-list":    sessionList,
	"session-revoke":  sessionRevoke,
	"key-rotate":      keyRotate,
	"key-activate":    keyActivate,
	"jwks-export":     jwksExport,
	"bootstrap":       bootstrap,
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
		env.Log.ErrorContext(ctx, "unknown iam command", "commands", Names())
		return 2
	}
	st, err := env.Open(ctx)
	if err != nil {
		env.Log.ErrorContext(ctx, "open iam store", "err", err)
		return 1
	}
	defer st.Close()
	if err := st.Ping(ctx); err != nil {
		env.Log.ErrorContext(ctx, "reach iam store", "err", err)
		return 1
	}
	if err := commands[args[0]](ctx, env, st, args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			env.Log.ErrorContext(ctx, "iam command usage", "command", args[0], "err", err)
			return 2
		}
		env.Log.ErrorContext(ctx, "iam command failed", "command", args[0], "err", err)
		return 1
	}
	return 0
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

func audit(env Env, tenant, action, account, session string) store.AuditEvent {
	return store.AuditEvent{TenantID: tenant, At: env.Now(), Actor: actor, Action: action, AccountID: account, SessionID: session}
}

func revocation(env Env, reason string) store.Revocation {
	return store.Revocation{By: actor, Reason: reason, At: env.Now()}
}
