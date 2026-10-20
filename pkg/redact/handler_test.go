package redact_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/redact"
)

func TestHandlerScrubsEveryAttrShape(t *testing.T) {
	mongoURI := "mongodb://chatim:pw1secret@m1:27017,m2:27017/?replicaSet=rs0&authMechanismProperties=AWS_SESSION_TOKEN:t0k3nsecret"
	natsURL := "nats://core:pw2secret@n1:4222"
	secrets := []string{"m0ng0pwsecret", "st4t3pwsecret", "d3dup3pwsecret"}
	var buf bytes.Buffer
	log := slog.New(redact.NewHandler(slog.NewJSONHandler(&buf, nil), []string{mongoURI, natsURL}, secrets))
	leak := fmt.Errorf("dial %s failed", natsURL)
	log.With("uri", mongoURI).WithGroup("g").Info("connect "+natsURL+" failed",
		"err", leak, "token", "t0k3nsecret", "steps", []string{"pw1secret", "d3dup3pwsecret"},
		"auth", errors.New("AUTH st4t3pwsecret failed"), "mongo", "sasl m0ng0pwsecret rejected",
		slog.Group("inner", "url", mongoURI), "count", 3)
	out := buf.String()
	for _, s := range append([]string{"pw1secret", "pw2secret", "t0k3nsecret"}, secrets...) {
		if strings.Contains(out, s) {
			t.Fatalf("log output leaks %q: %s", s, out)
		}
	}
	for _, want := range []string{"xxxxx@m1:27017,m2:27017", "xxxxx@n1:4222", `"count":3`, `"inner":{`, `"g":{`} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %s misses %s", out, want)
		}
	}
}

func TestHandlerKeepsTheInnerLevel(t *testing.T) {
	var buf bytes.Buffer
	h := redact.NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}), nil, nil)
	if h.Enabled(t.Context(), slog.LevelInfo) {
		t.Fatal("Enabled(info) = true, want the inner warn level kept")
	}
	if !h.Enabled(t.Context(), slog.LevelError) {
		t.Fatal("Enabled(error) = false, want true")
	}
}
