package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type closingDrainer struct {
	idle
	closed atomic.Bool
	err    error
	hold   time.Duration
}

func (d *closingDrainer) Close(context.Context) error {
	time.Sleep(d.hold)
	d.closed.Store(true)
	return d.err
}

func TestDrainerGroupStopsEveryMemberWhenOneFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var siblingStopped atomic.Bool
		sibling := idle{run: func(ctx context.Context) error {
			<-ctx.Done()
			siblingStopped.Store(true)
			return ctx.Err()
		}}
		failing := idle{run: func(context.Context) error { return errBroken }}
		if err := (drainerGroup{sibling, failing}).Run(t.Context()); !errors.Is(err, errBroken) || errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want only the failing member's error", err)
		}
		if !siblingStopped.Load() {
			t.Fatal("the sibling kept running after a member failed")
		}
	})
}

func TestDrainerGroupRunEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- (drainerGroup{idle{}, idle{}}).Run(ctx) }()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	})
}

func TestDrainerGroupClosesMembersTogether(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := &closingDrainer{hold: time.Second}, &closingDrainer{hold: time.Second, err: errBroken}
		began := time.Now()
		if err := (drainerGroup{a, b}).Close(t.Context()); !errors.Is(err, errBroken) {
			t.Fatalf("Close = %v, want the failing member's error", err)
		}
		if took := time.Since(began); took != time.Second {
			t.Fatalf("Close took %v, want the members closed in parallel within 1s", took)
		}
		if !a.closed.Load() || !b.closed.Load() {
			t.Fatal("a member was not closed")
		}
	})
}
