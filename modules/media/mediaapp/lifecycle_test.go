package mediaapp_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
)

func TestTheConsumerFetchesNothingWhileStorageIsNotReady(t *testing.T) {
	d := deps(t)
	synctest.Test(t, func(t *testing.T) {
		a, err := mediaapp.New(valid(), d)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- a.Run(t.Context()) }()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if got := d.JS.(*lookups).count(); got != 0 {
			t.Fatalf("looked up the consumer %d times while not ready", got)
		}
		if err := a.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if mediaapp.StopTimeout() != 1250*time.Millisecond {
			t.Fatalf("stop timeout %v", mediaapp.StopTimeout())
		}
	})
}

func TestBadConfigRunsNoConsumerUntilStopped(t *testing.T) {
	c := valid()
	c.Region = ""
	a, err := mediaapp.New(c, deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}
