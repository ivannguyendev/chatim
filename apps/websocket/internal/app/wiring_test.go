package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWiringPassesOneJWTLeewayToTheVerifierAndTheSessions(t *testing.T) {
	cfg := testConfig("redis:6379", "/keys.json")
	cfg.JWTLeeway = 42 * time.Second
	if got := jwtConfig(cfg); got.Leeway != cfg.JWTLeeway || got.Issuer != testIssuer || got.Audience != testAudience {
		t.Fatalf("jwt config = %+v, want leeway %v", got, cfg.JWTLeeway)
	}
	if got := sessionOptions(cfg).JWTLeeway; got != cfg.JWTLeeway {
		t.Fatalf("session leeway = %v, want %v", got, cfg.JWTLeeway)
	}
}

func TestSessionOptionsCopyEveryLimit(t *testing.T) {
	cfg := testConfig("redis:6379", "/keys.json")
	got := sessionOptions(cfg)
	if got.AuthTimeout != cfg.AuthTimeout || got.HeartbeatTimeout != cfg.HeartbeatTimeout || got.WriteTimeout != cfg.WriteTimeout ||
		got.UnauthMax != cfg.UnauthMax || got.FrameMax != cfg.FrameMax || got.CommandRate != cfg.CommandRate ||
		got.CommandBurst != cfg.CommandBurst || got.CommandInflight != cfg.CommandInflight || got.SendQueue != cfg.SendQueue {
		t.Fatalf("session options = %+v from %+v", got, cfg)
	}
}

func TestRouterConfigNamesTheNodeAndCarriesTheRedisSettings(t *testing.T) {
	cfg := testConfig("redis:6379", "/keys.json")
	cfg.RedisDB = 3
	got := routerConfig(cfg, Deps{ReadyTimeout: time.Second}, nil)
	if got.ClientName != "chatim-websocket-ws-test" || got.RedisAddr != "redis:6379" || got.RedisDB != 3 ||
		got.RedisPassword != testRedisPassword || got.ReadyTimeout != time.Second {
		t.Fatalf("router config = %+v", got)
	}
}

func TestWebsocketHTTPServerBoundsHeadersAndServesOnlyWS(t *testing.T) {
	hit := false
	srv := newHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }), newLogWatch().logger())
	if srv.ReadHeaderTimeout != 5*time.Second || srv.MaxHeaderBytes != 16<<10 {
		t.Fatalf("ReadHeaderTimeout %v, MaxHeaderBytes %d; want 5s and 16KB", srv.ReadHeaderTimeout, srv.MaxHeaderBytes)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusNotFound || hit {
		t.Fatalf("/other = %d, handler hit %v", rec.Code, hit)
	}
	srv.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", nil))
	if !hit {
		t.Fatal("/ws did not reach the session server")
	}
}

func TestRouterHolderAnswersUnavailableBeforeTheRouterIsOpen(t *testing.T) {
	h := &routerHolder{}
	h.Trigger()
	if _, err := h.Room("42"); status.Code(err) != codes.Unavailable {
		t.Fatalf("Room = %v, want UNAVAILABLE", err)
	}
	if _, err := h.Any(); status.Code(err) != codes.Unavailable {
		t.Fatalf("Any = %v, want UNAVAILABLE", err)
	}
}
