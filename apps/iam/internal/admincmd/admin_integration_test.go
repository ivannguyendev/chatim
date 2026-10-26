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
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
)

var tempPassword = regexp.MustCompile(`temporary password: (\S+)`)

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
	env, out, logs := testEnv(t, func(ctx context.Context) (admincmd.Store, error) { return pgstore.Open(ctx, uri, 2) })
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
	s, err := pgstore.Open(context.Background(), h.uri, 2)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(s.Close)
	return s
}

func TestAccountLifecycleCommands(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if out := h.run("migrate"); !strings.Contains(out, "pending: 0001_init.sql") {
		t.Fatalf("dry run = %q", out)
	}
	h.run("migrate", "-apply")
	if out := h.run("migrate"); !strings.Contains(out, "up to date") {
		t.Fatalf("after apply = %q", out)
	}
	h.run("tenant-create", "-id", "shop", "-name", "Shop")
	out := h.run("account-create", "-tenant", "shop", "-username", "Lan", "-email", "Lan@Shop.VN")
	first := tempPassword.FindStringSubmatch(out)
	if first == nil {
		t.Fatalf("account-create output = %q, want a temporary password", out)
	}
	st := h.store()
	a, err := st.AccountByUsername(ctx, "shop", "lan")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if ok, _ := h.env.Hasher.Verify(a.PasswordHash, first[1]); !ok || !a.PasswordMustChange || a.Email != "lan@shop.vn" {
		t.Fatalf("account = %+v; want the temporary password, forced change and a lowercase email", a)
	}
	second := tempPassword.FindStringSubmatch(h.run("password-reset", "-tenant", "shop", "-username", "lan"))
	if second == nil || second[1] == first[1] {
		t.Fatal("password-reset did not issue a new temporary password")
	}
	h.run("account-disable", "-tenant", "shop", "-username", "lan")
	if a, _ = st.AccountByUsername(ctx, "shop", "lan"); a.State != store.StateDisabled {
		t.Fatalf("state = %d, want disabled", a.State)
	}
	h.run("account-enable", "-tenant", "shop", "-username", "lan")
	h.run("tenant-disable", "-id", "shop")
	if tn, _ := st.TenantByID(ctx, "shop"); tn.State != store.StateDisabled {
		t.Fatalf("tenant state = %d, want disabled", tn.State)
	}
	if code := admincmd.Run(ctx, h.env, []string{"tenant-create", "-id", "shop", "-name", "Shop"}); code != 1 {
		t.Fatalf("duplicate tenant = %d, want 1", code)
	}
	for _, secret := range []string{first[1], second[1]} {
		if strings.Contains(h.logs.String(), secret) {
			t.Fatal("a temporary password reached the log")
		}
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
	h.run("key-activate", "-kid", pending.ID)
	keys, _ = st.SigningKeys(ctx)
	if keys[0].State != store.KeyRetired || keys[1].State != store.KeyActive {
		t.Fatalf("states = %d, %d; want retired then active", keys[0].State, keys[1].State)
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
