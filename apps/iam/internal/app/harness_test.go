package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/iam/internal/app"
	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/password"
	"github.com/ivannguyendev/chatim/apps/iam/internal/signing"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const waitLimit = 10 * time.Second

var fastHash = password.Params{Memory: 64, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32}

type fakeStore struct {
	store.Auth
	keys     []store.SigningKey
	version  int
	closed   atomic.Bool
	snapshot revocation.Snapshot
	cleaned  chan struct{}
	due      string
	dueAsked chan time.Duration
	outage
}

func (f *fakeStore) ActivateDueSigningKey(_ context.Context, lead time.Duration, _ store.AuditEvent) (string, error) {
	if f.dueAsked != nil {
		select {
		case f.dueAsked <- lead:
		default:
		}
	}
	kid := f.due
	f.due = ""
	return kid, nil
}

func (f *fakeStore) Revocations(context.Context) (revocation.Snapshot, error) {
	if f.down.Load() {
		return revocation.Snapshot{}, errOpen
	}
	return f.snapshot, nil
}

func (f *fakeStore) DeleteExpiredRevocations(context.Context) (int64, error) {
	if f.cleaned != nil {
		select {
		case f.cleaned <- struct{}{}:
		default:
		}
	}
	return 2, nil
}

func (f *fakeStore) SigningKeys(context.Context) ([]store.SigningKey, error) {
	signal(f.reloads)
	if f.down.Load() {
		return nil, errOpen
	}
	return f.keys, nil
}

func (f *fakeStore) Ping(ctx context.Context) error {
	f.pingCount.Add(1)
	signal(f.pings)
	if f.hangPing.Load() {
		<-ctx.Done()
		f.pingsDone.Add(1)
		return ctx.Err()
	}
	if f.down.Load() {
		return errOpen
	}
	return nil
}

func (f *fakeStore) SchemaVersion(context.Context) (int, error) {
	if f.down.Load() {
		return 0, errOpen
	}
	return f.version, nil
}

func (f *fakeStore) Close() {
	f.closedAt.Store(f.seq.Add(1))
	f.closed.Store(true)
}

func testConfig(kek []byte) config.Config {
	return config.Config{
		ListenAddr: "api", InternalAddr: "internal", NodeSecret: testNodeSecret, AdminAddr: "admin", PublicURL: "http://127.0.0.1", PGURI: "postgres://iam@db/iam", PGMaxConns: 2,
		Issuer: "iss", Audience: "aud", AccessTTL: 15 * time.Minute, SessionIdle: time.Hour, SessionMax: 2 * time.Hour,
		RefreshGrace: 30 * time.Second, DPoPWindow: time.Minute, HashConcurrency: 2, LoginFreeFailures: 5, LoginMaxDelay: time.Minute,
		LoginIPRate: 10, LoginIPBurst: 10, LoginIPTracked: 100, PasswordMin: 12, KeyEncryptionKey: kek, KeyReload: time.Hour, KeyActivateLead: 2 * time.Hour,
		FeedWaitMax: 10 * time.Second, DirectoryLoadSlots: 1, InflightMax: 100, UserRate: 50, UserBurst: 100, UserInflight: 32, UserLimiters: 1000,
		BodyMax: 16 << 10, DrainDelay: 10 * time.Millisecond, ShutdownBudget: 20 * time.Second,
		ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 16 << 10,
	}
}

func newKEK(t *testing.T) []byte {
	t.Helper()
	kek := make([]byte, signing.KEKLength)
	_, _ = rand.Read(kek)
	return kek
}

func activeKey(t *testing.T, kek []byte) store.SigningKey {
	t.Helper()
	sealer, _ := signing.NewSealer(kek, rand.Reader)
	k, _ := signing.Generate(rand.Reader)
	sealed, err := sealer.Seal(k)
	if err != nil {
		t.Fatal(err)
	}
	return store.SigningKey{ID: k.ID, State: store.KeyActive, PublicKey: k.PublicKey().Key, PrivateSealed: sealed}
}

const testNodeSecret = "node-secret-value-0123456789abcdef"

type listeners struct {
	addrs map[string]chan string
}

func (l *listeners) listen(ctx context.Context, name string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l.addrs[name] <- lis.Addr().String()
	return lis, nil
}

type logWatch struct {
	messages chan string
	mu       sync.Mutex
	text     bytes.Buffer
}

func (w *logWatch) Enabled(context.Context, slog.Level) bool { return true }

func (w *logWatch) Handle(ctx context.Context, r slog.Record) error {
	w.mu.Lock()
	err := slog.NewJSONHandler(&w.text, nil).Handle(ctx, r)
	w.mu.Unlock()
	select {
	case w.messages <- r.Message:
	default:
	}
	return err
}

func (w *logWatch) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *logWatch) WithGroup(string) slog.Handler      { return w }

func (w *logWatch) await(t *testing.T, message string) {
	t.Helper()
	timeout := time.After(waitLimit)
	for {
		select {
		case got := <-w.messages:
			if got == message {
				return
			}
		case <-timeout:
			t.Fatalf("log %q not seen", message)
		}
	}
}

type running struct {
	cancel               context.CancelFunc
	done                 chan error
	admin, api, internal string
	logs                 *logWatch
}

func runIAM(t *testing.T, cfg config.Config, st *fakeStore, schema int, adjust ...func(*app.Deps)) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	lis := &listeners{addrs: map[string]chan string{"admin": make(chan string, 1), "api": make(chan string, 1), "internal": make(chan string, 1)}}
	logs := &logWatch{messages: make(chan string, 1024)}
	done := make(chan error, 1)
	stopped := make(chan struct{})
	deps := app.Deps{
		Listen:        lis.listen,
		OpenStore:     func(context.Context, string, pgstore.Options) (app.Store, error) { return st, nil },
		OpenListener:  openIdleListener,
		MemoryLimit:   func() int64 { return 256 << 20 },
		HashParams:    fastHash,
		SchemaVersion: schema,
	}
	for _, a := range adjust {
		a(&deps)
	}
	go func() {
		defer close(stopped)
		done <- app.Run(ctx, cfg, slog.New(logs), deps)
	}()
	r := &running{cancel: cancel, done: done, logs: logs}
	r.admin = receive(t, lis.addrs["admin"], "admin listen")
	if cfg.ListenAddr != "" {
		r.api = receive(t, lis.addrs["api"], "api listen")
	}
	if cfg.InternalAddr != "" {
		r.internal = receive(t, lis.addrs["internal"], "internal listen")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(waitLimit):
			t.Error("iam did not stop")
		}
	})
	return r
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitLimit):
		t.Fatalf("%s did not happen", what)
		var zero T
		return zero
	}
}

func httpGet(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	return httpGetWith(t, addr, path, nil)
}

func httpGetWith(t *testing.T, addr, path string, header http.Header) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, http.NoBody)
	maps.Copy(req.Header, header)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

var errOpen = errors.New("cannot reach postgres")
