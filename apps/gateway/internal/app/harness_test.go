package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
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

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/goleak"
	"google.golang.org/grpc"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/config"
	"github.com/ivannguyendev/chatim/pkg/iamsync/iamsynctest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/pb/chatim/v1/chatimv1connect"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	testIssuer   = "chatim-dev"
	testAudience = "chatim-gateway"
	testKID      = "k1"
	waitLimit    = 10 * time.Second
)

type signer struct {
	priv ed25519.PrivateKey
	path string
}

func newSigner(t *testing.T) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`, testKID, base64.RawURLEncoding.EncodeToString(pub))
	path := filepath.Join(t.TempDir(), "jwks.json")
	if err := os.WriteFile(path, []byte(jwks), 0o600); err != nil {
		t.Fatal(err)
	}
	return signer{priv: priv, path: path}
}

func (s signer) token(t *testing.T) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": testIssuer, "aud": testAudience, "sub": "u1", "tid": "t1", "sid": "s1", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
	tok.Header["kid"] = testKID
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

type fakeCore struct {
	chatimv1.UnimplementedCoreServiceServer
	started chan struct{}
	release chan struct{}
}

func (f *fakeCore) GetHistory(ctx context.Context, _ *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, error) {
	if f.release != nil {
		f.started <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &chatimv1.GetHistoryResponse{}, nil
}

func startCore(t *testing.T, core *fakeCore) string {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	return lis.Addr().String()
}

func deadAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

func testConfig(keysFile string, cores ...string) config.Config {
	return config.Config{
		ListenAddr: "api", AdminAddr: "admin", CoreAddrs: cores,
		JWTKeysFile: keysFile, JWTIssuer: testIssuer, JWTAudience: testAudience, JWTLeeway: time.Second,
		BodyMax: 128 << 10, UserRate: 50, UserBurst: 100, UserInflight: 32, UserLimiters: 1000, InflightMax: 100,
		DrainDelay: 100 * time.Millisecond, ShutdownBudget: 15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: time.Minute,
		MaxHeaderBytes: 16 << 10, H2MaxStreams: 250, RequestDeadline: 5 * time.Second, IAM: iamsynctest.Config(),
	}
}

type listeners struct {
	admin, api chan string
}

func newListeners() *listeners {
	return &listeners{admin: make(chan string, 1), api: make(chan string, 1)}
}

func (l *listeners) listen(ctx context.Context, name string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	map[string]chan string{"admin": l.admin, "api": l.api}[name] <- lis.Addr().String()
	return lis, nil
}

type logWatch struct {
	messages chan string
	mu       sync.Mutex
	text     bytes.Buffer
}

func newLogWatch() *logWatch { return &logWatch{messages: make(chan string, 1024)} }

func (w *logWatch) logger() *slog.Logger { return slog.New(w) }

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

func runAsync(ctx context.Context, cfg config.Config, log *slog.Logger, lis *listeners) chan error {
	return runAsyncWith(ctx, cfg, log, Deps{Listen: lis.listen})
}

func runAsyncWith(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) chan error {
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, log, deps) }()
	return done
}

func httpStatus(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, http.NoBody)
	if err != nil {
		t.Fatal(err)
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

func h2cClient(t *testing.T, addr, token string) chatimv1connect.CoreServiceClient {
	t.Helper()
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: &protocols, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	auth := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token)
			return next(ctx, req)
		}
	})
	return chatimv1connect.NewCoreServiceClient(&http.Client{Transport: transport}, "http://"+addr, connect.WithInterceptors(auth))
}

func run(ctx context.Context, cfg config.Config, log *slog.Logger, deps Deps) error {
	if deps.IAMDoer == nil {
		deps.IAMDoer = iamsynctest.Accepting{}
	}
	return Run(ctx, cfg, log, deps)
}
