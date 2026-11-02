package admincmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

func databaseURI(t *testing.T) string {
	t.Helper()
	base := os.Getenv("CHATIM_IT_PG_URI")
	if base == "" {
		t.Skip("set CHATIM_IT_PG_URI to run")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "iam_it_" + hex.EncodeToString(suffix)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = conn.Close(context.Background())
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	return u.String()
}

type harness struct {
	t    *testing.T
	env  admincmd.Env
	uri  string
	out  *bytes.Buffer
	logs *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	uri := databaseURI(t)
	env, out, logs := testEnv(t, func(ctx context.Context) (admincmd.Store, error) {
		return pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 2, RevocationKeep: 16 * time.Minute})
	})
	return &harness{t: t, env: env, uri: uri, out: out, logs: logs}
}

func (h *harness) run(args ...string) string {
	h.t.Helper()
	h.out.Reset()
	if code := admincmd.Run(context.Background(), h.env, args); code != 0 {
		h.t.Fatalf("Run(%v) = %d; logs: %s", args, code, h.logs.String())
	}
	return h.out.String()
}

func (h *harness) store() *pgstore.Store {
	h.t.Helper()
	s, err := pgstore.Open(context.Background(), h.uri, pgstore.Options{MaxConns: 2, RevocationKeep: 16 * time.Minute})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(s.Close)
	return s
}

func stateOf(t *testing.T, h *harness, sql string, args ...any) store.State {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.uri)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var state store.State
	if err := conn.QueryRow(context.Background(), sql, args...).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return state
}

func TestTenantAndUserStateCommands(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if out := h.run("migrate"); !strings.Contains(out, "pending: 0001_identity.sql") {
		t.Fatalf("dry run = %q", out)
	}
	h.run("migrate", "-apply")
	if out := h.run("migrate"); !strings.Contains(out, "up to date") {
		t.Fatalf("after apply = %q", out)
	}
	h.run("tenant-create", "-id", "shop", "-name", "Shop")
	seedUser(t, h, "shop", lanID, "lan")
	const userState = "SELECT state FROM users WHERE tenant_id = 'shop' AND id = $1"
	if out := h.run("user-disable", "-tenant", "shop", "-user", lanID); out != "user "+lanID+": user_disable\n" {
		t.Fatalf("user-disable output = %q", out)
	}
	if st := stateOf(t, h, userState, lanID); st != store.StateDisabled {
		t.Fatalf("state = %d, want disabled", st)
	}
	h.run("user-enable", "-tenant", "shop", "-user", lanID)
	if st := stateOf(t, h, userState, lanID); st != store.StateActive {
		t.Fatalf("state = %d, want active", st)
	}
	if code := admincmd.Run(ctx, h.env, []string{"user-disable", "-tenant", "shop", "-user", "nobody"}); code != 1 {
		t.Fatalf("user-disable of an unknown user = %d, want 1", code)
	}
	if code := admincmd.Run(ctx, h.env, []string{"user-disable", "-tenant", "blog", "-user", lanID}); code != 1 {
		t.Fatalf("user-disable through another tenant = %d, want 1", code)
	}
	h.run("tenant-disable", "-id", "shop")
	if st := stateOf(t, h, "SELECT state FROM tenants WHERE id = 'shop'"); st != store.StateDisabled {
		t.Fatalf("tenant state = %d, want disabled", st)
	}
	if code := admincmd.Run(ctx, h.env, []string{"tenant-create", "-id", "shop", "-name", "Shop"}); code != 1 {
		t.Fatalf("duplicate tenant = %d, want 1", code)
	}
}

func TestKeyCommandsAndBootstrap(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := t.TempDir()
	jwks := filepath.Join(dir, "jwks.json")
	dev := `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"dev","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo","alg":"EdDSA","use":"sig"}]}`
	if err := os.WriteFile(jwks, []byte(dev), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run("bootstrap", "-jwks", jwks)
	h.run("bootstrap", "-jwks", jwks)
	st := h.store()
	keys, _ := st.SigningKeys(ctx)
	if len(keys) != 1 || keys[0].State != store.KeyActive {
		t.Fatalf("keys after two bootstraps = %+v, want exactly one active key", keys)
	}
	if kids := readKIDs(t, jwks); len(kids) != 2 || kids[0] != "dev" || kids[1] != keys[0].ID {
		t.Fatalf("jwks kids = %v, want dev and the active key", kids)
	}
	h.run("key-rotate")
	keys, _ = st.SigningKeys(ctx)
	pending := keys[len(keys)-1]
	if pending.State != store.KeyPending {
		t.Fatalf("new key state = %d, want pending", pending.State)
	}
	h.env.ActivateLead = time.Hour
	if code := admincmd.Run(ctx, h.env, []string{"key-activate", "-kid", pending.ID}); code != 1 {
		t.Fatalf("key-activate inside the lead = %d, want 1", code)
	}
	if keys, _ = st.SigningKeys(ctx); keys[len(keys)-1].State != store.KeyPending {
		t.Fatal("a key activated inside the lead")
	}
	h.env.ActivateLead = 0
	h.run("key-activate", "-kid", pending.ID)
	keys, _ = st.SigningKeys(ctx)
	if keys[0].State != store.KeyRetired || keys[1].State != store.KeyActive {
		t.Fatalf("states = %d, %d; want retired then active", keys[0].State, keys[1].State)
	}
	if code := admincmd.Run(ctx, h.env, []string{"key-activate", "-kid", keys[0].ID}); code != 1 {
		t.Fatalf("key-activate of a retired key = %d, want 1", code)
	}
	if keys, _ = st.SigningKeys(ctx); keys[0].State != store.KeyRetired || keys[1].State != store.KeyActive {
		t.Fatalf("states after reactivating a retired key = %d, %d; want unchanged", keys[0].State, keys[1].State)
	}
	out := filepath.Join(dir, "export.json")
	h.run("jwks-export", "-out", out)
	if kids := readKIDs(t, out); len(kids) != 2 {
		t.Fatalf("export kids = %v, want the retired and the active key", kids)
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0o644 {
		t.Fatalf("export mode = %v, want 0644", info.Mode().Perm())
	}
}

func readKIDs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Keys []struct {
			KID string `json:"kid"`
			D   string `json:"d"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	kids := make([]string, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.D != "" {
			t.Fatal("exported JWKS contains a private key")
		}
		kids = append(kids, k.KID)
	}
	return kids
}
