package feed

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func closedPortURL(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return "nats://ws:n4tss3cr3t@" + addr
}

func TestDialGivesUpWhenNATSNeverAnswersAndHidesTheCredentials(t *testing.T) {
	url := closedPortURL(t)
	_, err := Dial(t.Context(), NATSConfig{URL: url, Name: "feed-test", ReadyTimeout: 300 * time.Millisecond}, quietLog())
	if !errors.Is(err, errNotConnected) {
		t.Fatalf("Dial = %v, want not connected", err)
	}
	if strings.Contains(err.Error(), "n4tss3cr3t") {
		t.Fatalf("Dial error leaks the password: %v", err)
	}
}

func TestDialStopsWaitingWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Dial(ctx, NATSConfig{URL: closedPortURL(t), Name: "feed-test", ReadyTimeout: time.Minute}, quietLog())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial = %v, want canceled", err)
	}
}
