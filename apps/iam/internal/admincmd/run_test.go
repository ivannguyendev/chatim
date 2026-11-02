package admincmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type closedStore struct {
	admincmd.Store
	pingErr error
	closed  *bool
}

func (s closedStore) Ping(context.Context) error { return s.pingErr }

func (s closedStore) Close() {
	if s.closed != nil {
		*s.closed = true
	}
}

func testEnv(t *testing.T, open func(context.Context) (admincmd.Store, error)) (admincmd.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	sealer, _ := signing.NewSealer(kek, rand.Reader)
	out, logs := &bytes.Buffer{}, &bytes.Buffer{}
	return admincmd.Env{
		Open: open, Sealer: sealer, Random: rand.Reader, Now: time.Now,
		KeyWindow: 15*time.Minute + 30*time.Second, Out: out, Log: slog.New(slog.NewJSONHandler(logs, nil)),
	}, out, logs
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	env, _, _ := testEnv(t, func(context.Context) (admincmd.Store, error) { return closedStore{}, nil })
	cases := [][]string{
		{},
		{"nope"},
		{"tenant-create", "-id", "shop"},
		{"tenant-create", "-id", "Bad Tenant", "-name", "x"},
		{"account-disable", "-tenant", "shop"},
		{"account-enable", "-tenant", "shop", "-username", "lan"},
		{"jwks-export"},
		{"migrate", "extra"},
		{"key-activate", "-unknown"},
		{"group-create", "-tenant", "shop", "-id", "sales"},
		{"group-update", "-tenant", "shop", "-id", "sales"},
		{"group-delete", "-tenant", "shop"},
		{"account-groups", "-tenant", "shop", "-user", "lan"},
		{"profile-set", "-tenant", "shop", "-user", "lan"},
	}
	for _, args := range cases {
		if code := admincmd.Run(context.Background(), env, args); code != 2 {
			t.Fatalf("Run(%v) = %d, want 2", args, code)
		}
	}
}

func TestIsKnowsEveryCommand(t *testing.T) {
	for _, name := range []string{"migrate", "account-disable", "account-groups", "profile-set", "key-rotate", "jwks-export", "bootstrap"} {
		if !admincmd.Is(name) {
			t.Fatalf("Is(%q) = false", name)
		}
	}
	for _, name := range []string{"account-create", "password-reset", "session-list", "session-revoke", "role-create", "account-roles", "group-roles", "client-permissions-set"} {
		if admincmd.Is(name) {
			t.Fatalf("Is(%q) = true for a removed command", name)
		}
	}
	if admincmd.Is("serve") || admincmd.Is("probe") {
		t.Fatal("serve and probe are not admin commands")
	}
}

type missingAccountStore struct {
	closedStore
	tenant, user string
}

func (s *missingAccountStore) SetAccountState(_ context.Context, tenantID, id string, _ store.State, _ store.AuditEvent) error {
	s.tenant, s.user = tenantID, id
	return apperr.ErrNotFound
}

func TestAccountDisableOfAnUnknownUserExitsWithOne(t *testing.T) {
	st := &missingAccountStore{}
	env, out, logs := testEnv(t, func(context.Context) (admincmd.Store, error) { return st, nil })
	if code := admincmd.Run(context.Background(), env, []string{"account-disable", "-tenant", "shop", "-user", "ghost"}); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if st.tenant != "shop" || st.user != "ghost" || out.Len() != 0 || !strings.Contains(logs.String(), "not found") {
		t.Fatalf("store saw %q/%q, out=%q logs=%s; want the user id passed through and not found logged", st.tenant, st.user, out, logs)
	}
}

func TestStoreOpenFailureExitsWithOne(t *testing.T) {
	env, _, _ := testEnv(t, func(context.Context) (admincmd.Store, error) { return nil, os.ErrNotExist })
	if code := admincmd.Run(context.Background(), env, []string{"migrate"}); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
}

func TestUnreachablePostgresFailsFastWithOne(t *testing.T) {
	closed := false
	env, out, logs := testEnv(t, func(context.Context) (admincmd.Store, error) {
		return closedStore{pingErr: errors.New("dial tcp: connection refused"), closed: &closed}, nil
	})
	if code := admincmd.Run(context.Background(), env, []string{"tenant-create", "-id", "shop", "-name", "Shop"}); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
	}
	if !closed || out.Len() != 0 || !strings.Contains(logs.String(), "connection refused") {
		t.Fatalf("closed=%v out=%q logs=%s; want the store closed, no output and the ping error logged", closed, out, logs)
	}
}

func TestExportToAMissingDirectoryLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent", "jwks.json")
	if err := admincmd.WriteAtomicForTest(missing, []byte("{}")); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
	if err := admincmd.WriteAtomicForTest(filepath.Join(dir, "jwks.json"), []byte(`{"keys":[]}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "jwks.json" {
		t.Fatalf("dir = %v, want only jwks.json", entries)
	}
}
