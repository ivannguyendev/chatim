package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/pkg/coreroute"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	testIssuer           = "chatim-dev"
	testAudience         = "chatim-websocket"
	testKID              = "k1"
	testRoom             = 42
	testRedisPassword    = "s3cr3t-redis-pass"
	testNATSPassword     = "n4ts-s3cr3t"
	testPresencePassword = "pr3s3nc3-s3cr3t"
	waitLimit            = 10 * time.Second
)

type signer struct {
	priv ed25519.PrivateKey
	path string
}

func newSigner(t *testing.T) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`, testKID, base64.RawURLEncoding.EncodeToString(pub))
	path := filepath.Join(t.TempDir(), "jwks.json")
	if err := os.WriteFile(path, []byte(jwks), 0o600); err != nil {
		t.Fatalf("write jwks: %v", err)
	}
	return signer{priv: priv, path: path}
}

func (s signer) token(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "sub": "u1", "tid": "t1", "sid": "s1", "iat": time.Now().Unix(), "exp": expiresAt.Unix(),
	})
	tok.Header["kid"] = testKID
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func testConfig(redisAddr, keysFile string) config.Config {
	return config.Config{
		NodeID: "ws-test", ListenAddr: "ws", AdminAddr: "admin",
		RedisAddr: redisAddr, RedisPassword: testRedisPassword,
		JWTKeysFile: keysFile, JWTIssuer: testIssuer, JWTAudience: testAudience, JWTLeeway: 30 * time.Second,
		AuthGrace: 3 * time.Minute, AuthRate: 1000, AuthBurst: 2000, AuthRetryMin: 5 * time.Second, AuthRetryMax: 30 * time.Second,
		UserConnectionsMax: 10, UnauthMax: 100, FrameMax: 128 << 10, HeartbeatTimeout: 15 * time.Second,
		CommandRate: 50, CommandBurst: 100, CommandInflight: 32, SendQueue: 256, SendQueueBytes: 1 << 20, WriteTimeout: 10 * time.Second,
		RequestDeadline: 5 * time.Second, DrainDelay: 100 * time.Millisecond, ShutdownBudget: 15 * time.Second,
		NATSURL: "nats://ws:" + testNATSPassword + "@nats:4222", EventStream: "CHATIM_EVT", EventRoot: "evt",
		PublishRoot: "websocket.publish", PublishStream: "CHATIM_WS", StreamReplicas: 1,
		FanoutWorkers: 2, FanoutQueue: 64, ReplayRate: 1000, ConsumeBatch: 100, ConsumeBytes: 1 << 20,
		RoomIDsInflight: 4, OpenPending: 10 * time.Second, ReplayMaxAge: 2 * time.Minute, ReloadRate: 1000,
		TypingRate: 2, TypingBurst: 4, TypingQueue: 64, TypingPendingMsgs: 128,
		PresenceRedisAddr: "presence:6379", PresenceRedisPassword: testPresencePassword, PresenceRedisTimeout: 500 * time.Millisecond,
		PresenceRedisPool: 4, PresenceTTL: 15 * time.Second, PresenceStampEvery: 2 * time.Second, PresenceFlush: 50 * time.Millisecond, PresenceBatch: 500,
		PresenceCache: 5 * time.Second, PresenceWatchMax: 200, IAM: iamsync.Defaults(),
	}
}

func slotRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.RequireAuth(testRedisPassword)
	_ = mr.Set(slotmap.CoreKey("core-1"), "core-1:9000")
	_, _ = mr.ZAdd(slotmap.CoreRegistryKey, slotmap.CoreExpiryScore(time.Now().Add(time.Hour)), "core-1")
	_ = mr.Set(slotmap.SlotKey(slotmap.Of(testRoom)), "core-1")
	return mr
}

type fakeCore struct {
	chatimv1.UnimplementedCoreServiceServer
	started chan struct{}
	release chan struct{}
}

func (f *fakeCore) EditMessage(ctx context.Context, _ *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, error) {
	f.started <- struct{}{}
	select {
	case <-f.release:
		return &chatimv1.EditMessageResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeCore) ListMyRoomIDs(context.Context, *chatimv1.ListMyRoomIDsRequest) (*chatimv1.ListMyRoomIDsResponse, error) {
	return &chatimv1.ListMyRoomIDsResponse{}, nil
}

func startFakeCore(t *testing.T) (*fakeCore, coreroute.Dialer) {
	t.Helper()
	core := &fakeCore{started: make(chan struct{}, 8), release: make(chan struct{})}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	chatimv1.RegisterCoreServiceServer(srv, core)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		<-served
	})
	dial := func(string) (chatimv1.CoreServiceClient, io.Closer, error) {
		cc, err := grpc.NewClient("passthrough:///core",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
		if err != nil {
			return nil, nil, err
		}
		return chatimv1.NewCoreServiceClient(cc), cc, nil
	}
	return core, dial
}

type listeners struct {
	admin, ws chan net.Listener
}

func newListeners() *listeners {
	return &listeners{admin: make(chan net.Listener, 1), ws: make(chan net.Listener, 1)}
}

func (l *listeners) listen(ctx context.Context, name string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	switch name {
	case "admin":
		l.admin <- lis
	case "ws":
		l.ws <- lis
	}
	return lis, nil
}

func awaitListener(t *testing.T, ch chan net.Listener) string {
	t.Helper()
	select {
	case lis := <-ch:
		return lis.Addr().String()
	case <-time.After(waitLimit):
		t.Fatal("listener not opened")
		return ""
	}
}

type logWatch struct {
	messages chan string
	mu       *sync.Mutex
	text     *bytes.Buffer
	json     slog.Handler
}

func newLogWatch() *logWatch {
	text := &bytes.Buffer{}
	return &logWatch{messages: make(chan string, 1024), mu: &sync.Mutex{}, text: text, json: slog.NewJSONHandler(text, &slog.HandlerOptions{Level: slog.LevelDebug})}
}

func (w *logWatch) logger() *slog.Logger { return slog.New(w) }

func (w *logWatch) Enabled(context.Context, slog.Level) bool { return true }

func (w *logWatch) Handle(ctx context.Context, r slog.Record) error {
	w.mu.Lock()
	err := w.json.Handle(ctx, r)
	w.mu.Unlock()
	select {
	case w.messages <- r.Message:
	default:
	}
	return err
}

func (w *logWatch) WithAttrs([]slog.Attr) slog.Handler { return w }

func (w *logWatch) WithGroup(string) slog.Handler { return w }

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

func (w *logWatch) written() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.String()
}

func httpStatus(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func runAsync(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) chan error {
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, log, deps) }()
	return done
}

func awaitRun(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(waitLimit):
		t.Fatal("Run did not return")
		return nil
	}
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
