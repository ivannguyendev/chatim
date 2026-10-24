package feed

import (
	"context"
	"errors"
	"net"
	"os"
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

func TestBrokerPublishReachesASubscriberUntilItUnsubscribes(t *testing.T) {
	url := os.Getenv("CHATIM_IT_NATS_URL")
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	b, err := Dial(t.Context(), NATSConfig{URL: url, Name: "feed-test", PendingMsgs: 16}, quietLog())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer b.Close()
	got := make(chan string, 4)
	unsubscribe, err := b.Subscribe("eph.*.*.typing", func(subject string, data []byte) { got <- subject + "=" + string(data) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := b.Publish("eph.t1.7.typing", []byte("x")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case v := <-got:
		if v != "eph.t1.7.typing=x" {
			t.Fatalf("received %q", v)
		}
	case <-time.After(itTimeout):
		t.Fatal("published message not received")
	}
	if err := unsubscribe(); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	probe := make(chan struct{}, 1)
	if _, err := b.Subscribe("eph.probe", func(string, []byte) { probe <- struct{}{} }); err != nil {
		t.Fatalf("Subscribe probe: %v", err)
	}
	if err := errors.Join(b.Publish("eph.t1.7.typing", []byte("y")), b.Publish("eph.probe", nil)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-probe:
	case <-time.After(itTimeout):
		t.Fatal("probe not received")
	}
	select {
	case v := <-got:
		t.Fatalf("received %q after unsubscribe", v)
	default:
	}
	b.Close()
	if err := b.Publish("eph.t1.7.typing", []byte("z")); err == nil {
		t.Fatal("Publish on a closed connection succeeded")
	}
}
