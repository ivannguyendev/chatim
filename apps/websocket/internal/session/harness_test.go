package session

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/typing"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func testOptions() Options {
	return Options{
		AuthGrace:        3 * time.Minute,
		AuthRate:         1000,
		AuthBurst:        2000,
		AuthRetryMin:     5 * time.Second,
		AuthRetryMax:     30 * time.Second,
		HeartbeatTimeout: 15 * time.Second,
		JWTLeeway:        30 * time.Second,
		WriteTimeout:     10 * time.Second,
		UnauthMax:        100,
		FrameMax:         1024,
		CommandRate:      50,
		CommandBurst:     100,
		CommandInflight:  32,
		SendQueue:        16,
		SendQueueBytes:   64 << 10,
		TypingRate:       2,
		TypingBurst:      4,
		PresenceWatchMax: 200,
	}
}

type fakeVerifier struct {
	mu     sync.Mutex
	tokens map[string]jwtverify.Claims
	errs   map[string]error
	holds  map[string]chan struct{}
	bad    error
	calls  atomic.Int64
}

func newFakeVerifier(t *testing.T) *fakeVerifier {
	return &fakeVerifier{tokens: map[string]jwtverify.Claims{}, errs: map[string]error{}, bad: malformedTokenError(t)}
}

func (v *fakeVerifier) Verify(token string) (jwtverify.Claims, error) {
	v.calls.Add(1)
	v.mu.Lock()
	hold := v.holds[token]
	v.mu.Unlock()
	if hold != nil {
		<-hold
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err, ok := v.errs[token]; ok {
		return jwtverify.Claims{}, err
	}
	claims, ok := v.tokens[token]
	if !ok {
		return jwtverify.Claims{}, v.bad
	}
	return claims, nil
}

func (v *fakeVerifier) issue(token, tenant, user string, expiresAt time.Time) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tokens[token] = jwtverify.Claims{Tenant: tenant, User: user, Session: "s-" + token, IssuedAt: time.Now(), ExpiresAt: expiresAt}
	return token
}

func (v *fakeVerifier) issueClaims(token string, c jwtverify.Claims) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tokens[token] = c
	return token
}

func (v *fakeVerifier) fail(token string, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.errs[token] = err
}

func malformedTokenError(t *testing.T) error {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":%q}]}`, base64.RawURLEncoding.EncodeToString(public))
	verifier, err := jwtverify.New(jwtverify.Config{JWKS: []byte(jwks), Issuer: "issuer", Audience: "audience"})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	_, err = verifier.Verify("not-a-token")
	if jwtverify.ReasonOf(err) != jwtverify.ReasonMalformed {
		t.Fatalf("expected a malformed token error, got %v", err)
	}
	return err
}

type fakeRevocations struct {
	list    *revocation.List
	unknown atomic.Bool
	asked   atomic.Int64
}

func newFakeRevocations(t *testing.T) *fakeRevocations {
	list, err := revocation.New(revocation.Config{Leeway: 30 * time.Second, MaxKeep: 17 * time.Minute, MaxAhead: time.Minute})
	if err != nil {
		t.Fatalf("revocation list: %v", err)
	}
	return &fakeRevocations{list: list}
}

func (f *fakeRevocations) Check(c jwtverify.Claims) revocation.Verdict { return f.list.Check(c) }

func (f *fakeRevocations) UnknownKey(string) bool {
	f.asked.Add(1)
	return f.unknown.Load()
}

type harness struct {
	t        *testing.T
	server   *Server
	revoked  *fakeRevocations
	verifier *fakeVerifier
	core     *fakeCore
	rooms    *interest.Table
	loader   *roomLoader
	typing   *typing.Service
	presence *fakePresence
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	return newHarnessWith(t, opts, func(rooms Interest) Interest { return rooms })
}

func newHarnessWith(t *testing.T, opts Options, wrap func(Interest) Interest) *harness {
	t.Helper()
	verifier, core, loader := newFakeVerifier(t), newFakeCore(), &roomLoader{rooms: map[string][]interest.Room{}}
	rooms := newInterestWith(t, loader)
	typist, presence := newTestTyping(t, rooms), &fakePresence{}
	revoked := newFakeRevocations(t)
	server, err := New(opts, verifier, revoked, core, wrap(rooms), typist, presence, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{t: t, server: server, revoked: revoked, verifier: verifier, core: core, rooms: rooms, loader: loader, typing: typist, presence: presence}
}

type testClient struct {
	gws.BuiltinEventHandler
	t          *testing.T
	conn       *gws.Conn
	raw        net.Conn
	responses  chan *chatimv1.ServerResponse
	frames     chan *chatimv1.ServerFrame
	pongs      chan struct{}
	done       chan struct{}
	serverDone chan struct{}
	closeCode  uint16
}

func (h *harness) dial(reading bool) *testClient {
	h.t.Helper()
	return h.dialWrapped(reading, func(c net.Conn) net.Conn { return c })
}

func (h *harness) dialWrapped(reading bool, wrap func(net.Conn) net.Conn) *testClient {
	h.t.Helper()
	clientSide, pipeSide := net.Pipe()
	serverSide := wrap(pipeSide)
	tc := &testClient{
		t:          h.t,
		raw:        clientSide,
		responses:  make(chan *chatimv1.ServerResponse, 256),
		frames:     make(chan *chatimv1.ServerFrame, 256),
		pongs:      make(chan struct{}, 16),
		done:       make(chan struct{}),
		serverDone: make(chan struct{}),
	}
	go func() {
		defer close(tc.serverDone)
		h.serveTestConn(serverSide)
	}()
	conn, resp, err := gws.NewClientFromConn(tc, &gws.ClientOption{
		Addr:          "ws://chatim.test/ws",
		RequestHeader: http.Header{"Sec-Websocket-Protocol": []string{Subprotocol}},
	}, clientSide)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	tc.conn = conn
	if reading {
		go conn.ReadLoop()
	}
	return tc
}

func (h *harness) serveTestConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		_ = conn.Close()
		return
	}
	if status := h.server.admit(req); status != http.StatusOK {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", status, http.StatusText(status))
		_ = conn.Close()
		return
	}
	socket, err := h.server.upgrader.UpgradeFromConn(conn, reader, req)
	h.server.serveUpgraded(socket, err)
}

func (tc *testClient) OnMessage(_ *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	var frame chatimv1.ServerFrame
	if err := proto.Unmarshal(message.Bytes(), &frame); err != nil {
		tc.t.Errorf("decode server frame: %v", err)
		return
	}
	if resp := frame.GetResponse(); resp != nil {
		tc.responses <- resp
		return
	}
	tc.frames <- &frame
}

func (tc *testClient) OnPong(*gws.Conn, []byte) { tc.pongs <- struct{}{} }

func (tc *testClient) OnClose(_ *gws.Conn, err error) {
	if closeErr, ok := errors.AsType[*gws.CloseError](err); ok {
		tc.closeCode = closeErr.Code
	}
	close(tc.done)
}

func (tc *testClient) frame(frame *chatimv1.ClientFrame) {
	payload, err := proto.Marshal(frame)
	if err != nil {
		tc.t.Fatalf("marshal: %v", err)
	}
	tc.write(payload)
}

func (tc *testClient) write(payload []byte) {
	_ = tc.conn.WriteMessage(gws.OpcodeBinary, payload)
}

func (tc *testClient) auth(token string) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Auth{Auth: &chatimv1.AuthFrame{Token: token}}})
}

func (tc *testClient) heartbeat() {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Heartbeat{Heartbeat: &chatimv1.HeartbeatFrame{}}})
}

func (tc *testClient) request(req *chatimv1.ClientRequest) {
	tc.frame(&chatimv1.ClientFrame{Frame: &chatimv1.ClientFrame_Request{Request: req}})
}

func (tc *testClient) send(id uint64, room string) {
	tc.request(&chatimv1.ClientRequest{Id: id, Request: &chatimv1.ClientRequest_SendMessage{
		SendMessage: &chatimv1.SendMessageRequest{RoomId: room, Cid: fmt.Sprintf("c-%d", id), Text: "hi"},
	}})
}

func (tc *testClient) history(id uint64) {
	tc.request(&chatimv1.ClientRequest{Id: id, Request: &chatimv1.ClientRequest_GetHistory{
		GetHistory: &chatimv1.GetHistoryRequest{RoomId: "77"},
	}})
}

func (tc *testClient) next() *chatimv1.ServerResponse {
	tc.t.Helper()
	select {
	case resp := <-tc.responses:
		return resp
	case <-tc.done:
		tc.t.Fatalf("connection closed with %d while waiting for a response", tc.closeCode)
		return nil
	}
}

func (tc *testClient) waitClosed() uint16 {
	<-tc.done
	<-tc.serverDone
	return tc.closeCode
}

func (tc *testClient) isOpen() bool {
	select {
	case <-tc.done:
		return false
	default:
		return true
	}
}

func (tc *testClient) close() {
	_ = tc.conn.WriteClose(1000, nil)
	tc.waitClosed()
}
