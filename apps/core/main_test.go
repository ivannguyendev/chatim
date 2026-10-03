package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

var quiet = slog.New(slog.DiscardHandler)

func TestRealMainRejectsUnknownCommands(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"serve", "extra"}, {"probe", "extra"}} {
		if got := realMain(args); got != 2 {
			t.Errorf("realMain(%v) = %d, want 2", args, got)
		}
	}
}

func TestRealMainServeFailsOnInvalidConfig(t *testing.T) {
	t.Setenv("MONGO_URI", "")
	if got := realMain(nil); got != 1 {
		t.Fatalf("realMain() = %d, want 1 for a missing MONGO_URI", got)
	}
}

func TestRunFailsFastWhenMongoIsUnreachable(t *testing.T) {
	t.Setenv("MONGO_URI", "mongodb://chatim:s3cr3t@127.0.0.1:1/?directConnection=true")
	t.Setenv("CORE_ID", "core-unreachable")
	t.Setenv("CORE_CONNECT_TIMEOUT", "300ms")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	begin := time.Now()
	err = run(t.Context(), cfg, quiet)
	if err == nil {
		t.Fatal("run = nil, want a connect error")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("run took %v to fail, want it bounded by CORE_CONNECT_TIMEOUT", took)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("run error %q leaks the mongo password", err)
	}
}

func TestConnectErrorsHideCredentials(t *testing.T) {
	cfg := config.Config{
		CoreID:         "core-redact",
		ConnectTimeout: 300 * time.Millisecond,
		MongoURI:       "mongodb://chatim:s3cr3t@[bad/?replicaSet=rs0",
		NATSURL:        "nats://core:s3cr3t@[bad",
	}
	c := &clients{}
	defer c.close(t.Context(), quiet)
	for name, err := range map[string]error{
		"mongo": c.connectMongo(t.Context(), cfg),
		"nats":  c.connectNATS(cfg, quiet),
	} {
		if err == nil {
			t.Fatalf("%s connect = nil, want an error for a malformed url", name)
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Fatalf("%s connect error %q leaks the password", name, err)
		}
	}
}
