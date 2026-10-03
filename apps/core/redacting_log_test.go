package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

var leakyConfig = config.Config{
	CoreID:              "core-redact",
	ConnectTimeout:      time.Second,
	MongoURI:            "mongodb://chatim:pw1secret@m1:27017,m2:27017/?replicaSet=rs0&authMechanismProperties=AWS_SESSION_TOKEN:t0k3nsecret",
	NATSURL:             "nats://core:pw2secret@n1:4222",
	MongoPassword:       "m0ng0pwsecret",
	RedisPassword:       "st4t3pwsecret",
	RedisDedupePassword: "d3dup3pwsecret",
}

var leakySecrets = []string{"pw1secret", "pw2secret", "t0k3nsecret", "st4t3pwsecret", "d3dup3pwsecret", "m0ng0pwsecret"}

func assertNoSecrets(t *testing.T, out string) {
	t.Helper()
	for _, s := range leakySecrets {
		if strings.Contains(out, s) {
			t.Fatalf("log output leaks %q: %s", s, out)
		}
	}
}

func TestRedactedLoggerScrubsEveryAttrShape(t *testing.T) {
	var buf bytes.Buffer
	log := redactedLogger(slog.New(slog.NewJSONHandler(&buf, nil)), leakyConfig)
	leak := fmt.Errorf("dial %s failed", leakyConfig.NATSURL)
	log.With("uri", leakyConfig.MongoURI).WithGroup("g").Info("connect "+leakyConfig.NATSURL+" failed",
		"err", leak, "token", "t0k3nsecret", "steps", []string{"pw1secret", "d3dup3pwsecret"},
		"auth", errors.New("AUTH st4t3pwsecret failed"), "mongo", "sasl m0ng0pwsecret rejected",
		slog.Group("inner", "url", leakyConfig.MongoURI), "count", 3)
	out := buf.String()
	assertNoSecrets(t, out)
	for _, want := range []string{"xxxxx@m1:27017,m2:27017", "xxxxx@n1:4222", `"count":3`, `"inner":{`} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %s misses %s", out, want)
		}
	}
}

func TestNATSCallbacksRedactErrors(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	var opts nats.Options
	for _, o := range natsOptions(leakyConfig, log) {
		if err := o(&opts); err != nil {
			t.Fatalf("apply nats option: %v", err)
		}
	}
	leak := fmt.Errorf("read from %s: connection reset", leakyConfig.NATSURL)
	opts.DisconnectedErrCB(nil, leak)
	opts.AsyncErrorCB(nil, nil, leak)
	out := buf.String()
	assertNoSecrets(t, out)
	for _, want := range []string{"nats disconnected", "nats async error"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %s misses %q", out, want)
		}
	}
}
