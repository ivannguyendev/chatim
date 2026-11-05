package actor_test

import (
	"testing"
	"time"
)

func TestRunningClosesOnceRunStarts(t *testing.T) {
	rg := newRig(t, baseConfig)
	select {
	case <-rg.Running():
		t.Fatal("Running closed before Run started")
	default:
	}
	rg.start(t)
	select {
	case <-rg.Running():
	case <-time.After(5 * time.Second):
		t.Fatal("Running still open after Run started")
	}
	if !rg.Started() {
		t.Fatal("Started() = false after Running closed")
	}
}

func TestRunningStaysClosedAfterASecondRun(t *testing.T) {
	rg := started(t, baseConfig)
	if err := rg.Run(t.Context()); err == nil {
		t.Fatal("second Run = nil, want already started")
	}
	select {
	case <-rg.Running():
	default:
		t.Fatal("Running reopened after a rejected second Run")
	}
}
