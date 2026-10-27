package api_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/goleak"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/api"
	"github.com/ivannguyendev/chatim/apps/gateway/internal/limit"
	"github.com/ivannguyendev/chatim/pkg/corecall"
	"github.com/ivannguyendev/chatim/pkg/grpcclient"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	testIssuer   = "chatim-dev"
	testAudience = "chatim-gateway"
	testKID      = "k1"
	testOrigin   = "https://chat.example.com"
)

type coreCall struct {
	method string
	md     metadata.MD
	body   []byte
}

type fakeCore struct {
	mu      sync.Mutex
	calls   []coreCall
	err     error
	reply   proto.Message
	started chan struct{}
	release chan struct{}
}

func (f *fakeCore) handle(_ any, stream grpc.ServerStream) error {
	method, _ := grpc.MethodFromServerStream(stream)
	var in emptypb.Empty
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	f.mu.Lock()
	f.calls = append(f.calls, coreCall{method: method, md: md, body: in.ProtoReflect().GetUnknown()})
	err, reply, started, release := f.err, f.reply, f.started, f.release
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
		select {
		case <-release:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	if err != nil {
		return err
	}
	if reply == nil {
		reply = &emptypb.Empty{}
	}
	return stream.SendMsg(reply)
}

func (f *fakeCore) set(fn func(*fakeCore)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeCore) recorded() []coreCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]coreCall(nil), f.calls...)
}

type limits struct {
	rate, burst, inflight, node int
}

var roomy = limits{rate: 1000, burst: 1000, inflight: 32, node: 100}

type harness struct {
	core    *fakeCore
	handler *api.Handler
	server  *httptest.Server
	priv    ed25519.PrivateKey
	logs    *syncBuffer
	panics  atomic.Bool
	served  chan struct{}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newHarness(t *testing.T, l limits) *harness {
	t.Helper()
	h := &harness{core: &fakeCore{}, logs: &syncBuffer{}, served: make(chan struct{}, 64)}
	conn := startCore(t, h.core)
	fwd, err := corecall.New(corecall.Options{
		Methods:  corecall.Request(),
		Deadline: 2 * time.Second,
		Route: func(corecall.Command, proto.Message) (corecall.Conn, error) {
			if h.panics.Load() {
				panic("route failed")
			}
			return conn, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h.priv = priv
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`, testKID, base64.RawURLEncoding.EncodeToString(pub))
	verifier, err := jwtverify.New(jwtverify.Config{JWKS: []byte(jwks), Issuer: testIssuer, Audience: testAudience, Leeway: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := limit.NewGate(l.node)
	if err != nil {
		t.Fatal(err)
	}
	users, err := limit.NewUsers(limit.UserConfig{Rate: l.rate, Burst: l.burst, Inflight: l.inflight, Capacity: 1000})
	if err != nil {
		t.Fatal(err)
	}
	h.handler, err = api.New(api.Options{
		Forwarder: fwd, Verifier: verifier, Gate: gate, Users: users, BodyMax: 4 << 10,
		CORSOrigins: []string{testOrigin}, Log: slog.New(slog.NewJSONHandler(h.logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			select {
			case h.served <- struct{}{}:
			default:
			}
		}()
		h.handler.ServeHTTP(w, r)
	}))
	h.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	h.server.Start()
	t.Cleanup(h.server.Close)
	return h
}

func startCore(t *testing.T, core *fakeCore) corecall.Conn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(core.handle))
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(lis)
	}()
	dialer := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	cc, err := grpcclient.New("passthrough:///core", grpcclient.Options{Creds: insecure.NewCredentials(), DialOptions: []grpc.DialOption{dialer}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cc.Close()
		srv.Stop()
		<-served
	})
	return corecall.Conn{Core: chatimv1.NewCoreServiceClient(cc), Mention: chatimv1.NewMentionServiceClient(cc)}
}

type claims struct {
	tenant, user, issuer, audience string
	expires                        time.Time
}

func (h *harness) token(t *testing.T, c claims) string {
	t.Helper()
	if c.tenant == "" {
		c.tenant = "t1"
	}
	if c.user == "" {
		c.user = "u1"
	}
	if c.issuer == "" {
		c.issuer = testIssuer
	}
	if c.audience == "" {
		c.audience = testAudience
	}
	if c.expires.IsZero() {
		c.expires = time.Now().Add(time.Hour)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"iss": c.issuer, "aud": c.audience, "sub": c.user, "tid": c.tenant, "sid": "s1", "iat": c.expires.Add(-time.Hour).Unix(), "exp": c.expires.Unix()})
	tok.Header["kid"] = testKID
	signed, err := tok.SignedString(h.priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func (h *harness) post(t *testing.T, path, contentType, auth string, body []byte, header http.Header) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", contentType)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return h.do(t, req)
}

func (h *harness) do(t *testing.T, req *http.Request) reply {
	t.Helper()
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: raw}
}

func (h *harness) read(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	for _, s := range h.handler.Sources() {
		if s.Name != name || len(s.Labels) != len(labels) {
			continue
		}
		match := true
		for k, v := range labels {
			match = match && s.Labels[k] == v
		}
		if match {
			return s.Read()
		}
	}
	t.Fatalf("no source %s %v", name, labels)
	return 0
}
