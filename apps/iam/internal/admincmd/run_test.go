package admincmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
)

type closedStore struct {
	admincmd.Store
}

func (closedStore) Close() {}

func testEnv(t *testing.T, open func(context.Context) (admincmd.Store, error)) (admincmd.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	sealer, _ := signing.NewSealer(kek, rand.Reader)
	hasher, err := password.NewHasher(password.Params{Memory: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	out, logs := &bytes.Buffer{}, &bytes.Buffer{}
	return admincmd.Env{
		Open: open, Sealer: sealer, Hasher: hasher, Random: rand.Reader, Now: time.Now,
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
		{"account-create", "-tenant", "shop", "-username", "has space"},
		{"account-create", "-tenant", "shop", "-username", "lan", "-email", "no-at"},
		{"jwks-export"},
		{"migrate", "extra"},
		{"key-activate", "-unknown"},
	}
	for _, args := range cases {
		if code := admincmd.Run(context.Background(), env, args); code != 2 {
			t.Fatalf("Run(%v) = %d, want 2", args, code)
		}
	}
}

func TestIsKnowsEveryCommand(t *testing.T) {
	for _, name := range []string{"migrate", "account-create", "password-reset", "key-rotate", "jwks-export", "bootstrap"} {
		if !admincmd.Is(name) {
			t.Fatalf("Is(%q) = false", name)
		}
	}
	if admincmd.Is("serve") || admincmd.Is("probe") {
		t.Fatal("serve and probe are not admin commands")
	}
}

func TestStoreOpenFailureExitsWithOne(t *testing.T) {
	env, _, _ := testEnv(t, func(context.Context) (admincmd.Store, error) { return nil, os.ErrNotExist })
	if code := admincmd.Run(context.Background(), env, []string{"migrate"}); code != 1 {
		t.Fatalf("Run = %d, want 1", code)
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
