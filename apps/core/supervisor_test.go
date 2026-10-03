package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var errBroken = errors.New("broken")

func TestSupervisorReportsAnUnexpectedFailure(t *testing.T) {
	sup := newSupervisor(t.Context(), 2)
	healthy := sup.start("healthy", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	sup.start("broken", func(context.Context) error { return errBroken })
	err := sup.wait(t.Context())
	if !errors.Is(err, errBroken) || !strings.Contains(err.Error(), "broken stopped unexpectedly") {
		t.Fatalf("wait = %v, want the broken task's failure", err)
	}
	sup.stopping.Store(true)
	if err := healthy.stop(t.Context()); err != nil {
		t.Fatalf("stop healthy = %v, want nil for a cancelled task", err)
	}
}

func TestSupervisorReportsAnEarlyCleanExit(t *testing.T) {
	sup := newSupervisor(t.Context(), 1)
	sup.start("quitter", func(context.Context) error { return nil })
	if err := sup.wait(t.Context()); !errors.Is(err, errExited) {
		t.Fatalf("wait = %v, want %v", err, errExited)
	}
}

func TestSupervisorIgnoresExitsDuringShutdown(t *testing.T) {
	sup := newSupervisor(t.Context(), 1)
	task := sup.start("worker", func(ctx context.Context) error {
		<-ctx.Done()
		return errBroken
	})
	sup.stopping.Store(true)
	if err := task.stop(t.Context()); !errors.Is(err, errBroken) {
		t.Fatalf("stop = %v, want the task's own error", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := sup.wait(ctx); err != nil {
		t.Fatalf("wait = %v, want no failure reported while stopping", err)
	}
}

func TestSupervisorKeepsRunningPastTheParentContext(t *testing.T) {
	parent, cancelParent := context.WithCancel(t.Context())
	sup := newSupervisor(parent, 1)
	task := sup.start("worker", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	cancelParent()
	select {
	case <-task.done:
		t.Fatal("task stopped with the parent context, want it to wait for its own stop")
	case <-time.After(20 * time.Millisecond):
	}
	sup.stopping.Store(true)
	if err := task.stop(t.Context()); err != nil {
		t.Fatalf("stop = %v", err)
	}
}

func TestTaskStopIsBoundedByItsContext(t *testing.T) {
	release := make(chan struct{})
	sup := newSupervisor(t.Context(), 1)
	sup.stopping.Store(true)
	task := sup.start("stuck", func(context.Context) error {
		<-release
		return nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := task.stop(ctx)
	close(release)
	<-task.done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop = %v, want a deadline error for a task that ignores cancellation", err)
	}
}
