package memberwatch

import (
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestRealNATSMemberRemovedForgetsItsRoom(t *testing.T) {
	url := os.Getenv("CHATIM_IT_NATS_URL")
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	defer nc.Close()
	got := make(chan uint64, 64)
	w, err := New(nc, "live", func(room uint64) {
		select {
		case got <- room:
		default:
		}
	}, quiet)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush subscriptions: %v", err)
	}
	if err := nc.Publish("live.acme.member.101.evt.member_removed", []byte("not a protobuf")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case room := <-got:
			if room == 101 {
				return
			}
		case <-deadline:
			t.Fatal("forget(101) never called")
		}
	}
}
