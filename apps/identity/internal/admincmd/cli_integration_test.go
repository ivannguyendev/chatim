package admincmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/admincmd"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
)

const keySeparator = "│"

type harness struct {
	t    *testing.T
	env  admincmd.Env
	opts mongostore.Options
	db   *mongo.Database
	out  *bytes.Buffer
	logs *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		t.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	opts := mongostore.Options{URI: uri, Database: "identity_it_" + hex.EncodeToString(suffix), AppName: "chatim-identity-it", RevocationKeep: 16 * time.Minute}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	db := client.Database(opts.Database)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", opts.Database, err)
		}
		_ = client.Disconnect(ctx)
	})
	env, out, logs := testEnv(t, func(context.Context) (admincmd.Store, error) {
		st, err := mongostore.Open(opts)
		if err != nil {
			return nil, err
		}
		return st, nil
	})
	return &harness{t: t, env: env, opts: opts, db: db, out: out, logs: logs}
}

func (h *harness) run(args ...string) string {
	h.t.Helper()
	h.out.Reset()
	if code := admincmd.Run(context.Background(), h.env, args); code != 0 {
		h.t.Fatalf("Run(%v) = %d; logs: %s", args, code, h.logs.String())
	}
	return h.out.String()
}

func (h *harness) store() *mongostore.Store {
	h.t.Helper()
	s, err := mongostore.Open(h.opts)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(s.Close)
	return s
}

func (h *harness) find(collection, id string, out any) {
	h.t.Helper()
	if err := h.db.Collection(collection).FindOne(context.Background(), bson.D{{Key: "_id", Value: id}}).Decode(out); err != nil {
		h.t.Fatalf("read %s %s: %v", collection, id, err)
	}
}

func (h *harness) stateOf(collection, id string) store.State {
	h.t.Helper()
	var doc struct {
		State store.State `bson:"state"`
	}
	h.find(collection, id, &doc)
	return doc.State
}

func TestTenantAndUserStateCommands(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if out := h.run("migrate"); !strings.Contains(out, "collection users absent") || strings.Contains(out, "up to date") {
		t.Fatalf("dry run = %q", out)
	}
	if names, _ := h.db.ListCollectionNames(ctx, bson.D{}); len(names) != 0 {
		t.Fatalf("dry run created %v", names)
	}
	if out := h.run("migrate", "-apply"); !strings.Contains(out, "applied: collection users absent") || !strings.HasSuffix(out, "schema is up to date\n") {
		t.Fatalf("apply = %q", out)
	}
	if out := h.run("migrate"); out != "schema is up to date\n" {
		t.Fatalf("after apply = %q", out)
	}
	h.run("tenant-create", "-id", "shop", "-name", "Shop")
	seedUser(t, h, "shop", lanID, "lan")
	if out := h.run("user-disable", "-tenant", "shop", "-user", lanID); out != "user "+lanID+": user_disable\n" {
		t.Fatalf("user-disable output = %q", out)
	}
	if st := h.stateOf("users", "shop"+keySeparator+lanID); st != store.StateDisabled {
		t.Fatalf("state = %d, want disabled", st)
	}
	h.run("user-enable", "-tenant", "shop", "-user", lanID)
	if st := h.stateOf("users", "shop"+keySeparator+lanID); st != store.StateActive {
		t.Fatalf("state = %d, want active", st)
	}
	if code := admincmd.Run(ctx, h.env, []string{"user-disable", "-tenant", "shop", "-user", "nobody"}); code != 1 {
		t.Fatalf("user-disable of an unknown user = %d, want 1", code)
	}
	if code := admincmd.Run(ctx, h.env, []string{"user-disable", "-tenant", "blog", "-user", lanID}); code != 1 {
		t.Fatalf("user-disable through another tenant = %d, want 1", code)
	}
	h.run("tenant-disable", "-id", "shop")
	if st := h.stateOf("tenants", "shop"); st != store.StateDisabled {
		t.Fatalf("tenant state = %d, want disabled", st)
	}
	if code := admincmd.Run(ctx, h.env, []string{"tenant-create", "-id", "shop", "-name", "Shop"}); code != 1 {
		t.Fatalf("duplicate tenant = %d, want 1", code)
	}
}

func TestWriteOnAnEmptyDatabaseCreatesNoCollection(t *testing.T) {
	h := newHarness(t)
	if logs := h.fail(1, "tenant-create", "-id", "shop", "-name", "Shop"); !strings.Contains(logs, "schema incomplete, run migrate -apply") {
		t.Fatalf("logs = %s; want the schema message", logs)
	}
	names, err := h.db.ListCollectionNames(context.Background(), bson.D{})
	if err != nil || len(names) != 0 {
		t.Fatalf("collections = %v, %v; want none", names, err)
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
	first := keys[0].ID
	if kids := readKIDs(t, jwks); len(kids) != 2 || kids[0] != "dev" || kids[1] != first {
		t.Fatalf("jwks kids = %v, want dev and the active key", kids)
	}
	h.run("key-rotate")
	states := keyStates(t, st)
	var pending string
	for id, state := range states {
		if id != first && state == store.KeyPending {
			pending = id
		}
	}
	if len(states) != 2 || pending == "" {
		t.Fatalf("keys after rotate = %v, want the active key and one pending key", states)
	}
	h.env.ActivateLead = time.Hour
	if code := admincmd.Run(ctx, h.env, []string{"key-activate", "-kid", pending}); code != 1 {
		t.Fatalf("key-activate inside the lead = %d, want 1", code)
	}
	if keyStates(t, st)[pending] != store.KeyPending {
		t.Fatal("a key activated inside the lead")
	}
	h.env.ActivateLead = 0
	h.run("key-activate", "-kid", pending)
	if states = keyStates(t, st); states[first] != store.KeyRetired || states[pending] != store.KeyActive {
		t.Fatalf("states = %v; want %s retired and %s active", states, first, pending)
	}
	if code := admincmd.Run(ctx, h.env, []string{"key-activate", "-kid", first}); code != 1 {
		t.Fatalf("key-activate of a retired key = %d, want 1", code)
	}
	if states = keyStates(t, st); states[first] != store.KeyRetired || states[pending] != store.KeyActive {
		t.Fatalf("states after reactivating a retired key = %v; want unchanged", states)
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

func keyStates(t *testing.T, st *mongostore.Store) map[string]store.KeyState {
	t.Helper()
	keys, err := st.SigningKeys(context.Background())
	if err != nil {
		t.Fatalf("SigningKeys: %v", err)
	}
	out := make(map[string]store.KeyState, len(keys))
	for _, k := range keys {
		out[k.ID] = k.State
	}
	return out
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
