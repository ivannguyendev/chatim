package jsconsume_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/core/jsconsume"
)

var errLookup = errors.New("lookup refused")

type refusingLookup struct {
	stream, name string
	calls        int
}

func (l *refusingLookup) Consumer(_ context.Context, stream, name string) (jetstream.Consumer, error) {
	l.stream, l.name = stream, name
	l.calls++
	return nil, errLookup
}

func TestPullSourceLooksUpItsDurableAndReportsAFailedLookup(t *testing.T) {
	l := &refusingLookup{}
	src := jsconsume.NewPullSource(l, "ORDERS", "billing-totals")
	ds, err := src.Fetch(t.Context(), 8, time.Second)
	if !errors.Is(err, errLookup) || len(ds) != 0 {
		t.Fatalf("Fetch = %d deliveries, %v; want the lookup error", len(ds), err)
	}
	if l.stream != "ORDERS" || l.name != "billing-totals" {
		t.Errorf("looked up %s/%s, want ORDERS/billing-totals", l.stream, l.name)
	}
}

func TestPullSourceDoesNothingOnceTheContextEnded(t *testing.T) {
	l := &refusingLookup{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := jsconsume.NewPullSource(l, "S", "c").Fetch(ctx, 8, time.Second); !errors.Is(err, context.Canceled) || l.calls != 0 {
		t.Errorf("Fetch on an ended context = %v after %d lookups; want context.Canceled and no lookup", err, l.calls)
	}
}
