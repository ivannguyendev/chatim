package timer_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type consumerRig struct {
	src      *fakeSource
	members  *fakeRepairer
	messages *fakeRepairer
	c        *timer.Consumer
	result   chan error
}

func newConsumerRig(t *testing.T, ctx context.Context) *consumerRig {
	t.Helper()
	rg := &consumerRig{src: &fakeSource{}, members: &fakeRepairer{}, messages: &fakeRepairer{}, result: make(chan error, 1)}
	rg.c = built(timer.NewConsumer(timer.ConsumerDeps{Source: rg.src, Members: rg.members, Messages: rg.messages}, timer.ConsumerConfig{}, (&testlog.Sink{}).Logger()))(t)
	go func() { rg.result <- rg.c.Run(ctx) }()
	return rg
}

func (rg *consumerRig) stop(t *testing.T) {
	t.Helper()
	if err := rg.c.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-rg.result; err != nil {
		t.Fatalf("Run after Close = %v, want nil", err)
	}
}

func TestConsumerAcksRepairsDropsGoneTargetsAndTermsGarbage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		rg.messages.results = map[uint64]error{2: domain.ErrMessageNotFound}
		ok, gone := delivery(messageCheck(room, 1, pbconv.ReactionsCounter), 1), delivery(messageCheck(room, 2, pbconv.RepliesCounter), 1)
		members, garbage := delivery(memberCheck(room), 1), &fakeDelivery{data: []byte("junk")}
		rg.src.push(ok, gone, members, garbage)
		synctest.Wait()
		for name, d := range map[string]*fakeDelivery{"repaired": ok, "gone": gone, "members": members} {
			if acks, terms, naks := d.settled(); acks != 1 || terms != 0 || len(naks) != 0 {
				t.Errorf("%s: acks %d terms %d naks %v; want one ack", name, acks, terms, naks)
			}
		}
		if acks, terms, naks := garbage.settled(); acks != 0 || terms != 1 || len(naks) != 0 {
			t.Errorf("garbage: acks %d terms %d naks %v; want one term", acks, terms, naks)
		}
		if got := rg.members.checks(); !slices.Equal(got, []timer.Check{memberCheck(room)}) || len(rg.messages.checks()) != 2 {
			t.Errorf("member repairs %v, message repairs %v; want each check routed by its counter", got, rg.messages.checks())
		}
		if rg.c.Dropped() != 2 || rg.c.Failures() != 0 {
			t.Errorf("dropped %d failures %d; want the gone target and the garbage dropped", rg.c.Dropped(), rg.c.Failures())
		}
		rg.stop(t)
	})
}

func TestConsumerNaksAFailedRepairOnTheRetryLadder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		rg.messages.results = map[uint64]error{1: errBoom, 2: domain.ErrRetryLater}
		first, third := delivery(messageCheck(room, 1, pbconv.ReactionsCounter), 0), delivery(messageCheck(room, 2, pbconv.ReactionsCounter), 3)
		rg.src.push(first, third)
		synctest.Wait()
		if _, _, naks := first.settled(); !slices.Equal(naks, []time.Duration{5 * time.Second}) {
			t.Errorf("first delivery naks %v, want 5s", naks)
		}
		if _, _, naks := third.settled(); !slices.Equal(naks, []time.Duration{2 * time.Minute}) {
			t.Errorf("third delivery naks %v, want 2m", naks)
		}
		if rg.c.Failures() != 2 || rg.c.Dropped() != 0 {
			t.Errorf("failures %d dropped %d; want 2 failures", rg.c.Failures(), rg.c.Dropped())
		}
		rg.stop(t)
	})
}

func TestConsumerStopFinishesTheRunningRepairAndHandsTheRestBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		held, release := rg.messages.holdNext()
		running, waiting := delivery(messageCheck(room, 1, pbconv.ReactionsCounter), 1), delivery(messageCheck(room, 2, pbconv.ReactionsCounter), 1)
		rg.src.push(running, waiting, delivery(memberCheck(room), 1))
		<-held
		closed := make(chan error, 1)
		go func() { closed <- rg.c.Close(t.Context()) }()
		synctest.Wait()
		close(release)
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if err := <-rg.result; err != nil {
			t.Fatalf("Run = %v", err)
		}
		if acks, _, _ := running.settled(); acks != 1 {
			t.Errorf("running repair acks %d, want 1", acks)
		}
		if acks, _, naks := waiting.settled(); acks != 0 || !slices.Equal(naks, []time.Duration{0}) {
			t.Errorf("waiting check acks %d naks %v, want it handed back with Nak(0)", acks, naks)
		}
		if len(rg.members.checks()) != 0 {
			t.Errorf("member repair ran after the stop")
		}
	})
}

func TestConsumerCancelledMidRepairHandsTheCheckBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rg := newConsumerRig(t, ctx)
		held, _ := rg.messages.holdNext()
		d := delivery(messageCheck(room, 1, pbconv.ReactionsCounter), 2)
		rg.src.push(d)
		<-held
		cancel()
		if err := <-rg.result; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run after cancel = %v, want context.Canceled", err)
		}
		if _, _, naks := d.settled(); !slices.Equal(naks, []time.Duration{0}) || rg.c.Failures() != 0 {
			t.Fatalf("naks %v failures %d; want the check handed back without a failure", naks, rg.c.Failures())
		}
		if err := rg.c.Close(t.Context()); err != nil {
			t.Fatalf("Close after Run returned = %v", err)
		}
	})
}

func TestConsumerPausesAfterAFetchError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := &consumerRig{src: &fakeSource{errs: []error{errBoom, errBoom}}, members: &fakeRepairer{}, messages: &fakeRepairer{}, result: make(chan error, 1)}
		sink := &testlog.Sink{}
		rg.c = built(timer.NewConsumer(timer.ConsumerDeps{Source: rg.src, Members: rg.members, Messages: rg.messages}, timer.ConsumerConfig{}, sink.Logger()))(t)
		go func() { rg.result <- rg.c.Run(t.Context()) }()
		synctest.Wait()
		if n := rg.src.fetchCount(); n != 1 {
			t.Fatalf("fetches before the pause = %d, want 1", n)
		}
		time.Sleep(timer.DefaultFetchWait)
		synctest.Wait()
		if n := rg.src.fetchCount(); n != 2 {
			t.Fatalf("fetches after one pause = %d, want 2", n)
		}
		if sink.Count("due timer fetch failed; retrying") != 1 {
			t.Fatalf("fetch failures logged %d times, want once within a second", sink.Count("due timer fetch failed; retrying"))
		}
		rg.stop(t)
	})
}

func TestNewConsumerRefusesBadInputAndASecondRun(t *testing.T) {
	r := &fakeRepairer{}
	bad := map[string]func() (*timer.Consumer, error){
		"no source": func() (*timer.Consumer, error) {
			return timer.NewConsumer(timer.ConsumerDeps{Members: r, Messages: r}, timer.ConsumerConfig{}, nil)
		},
		"no members": func() (*timer.Consumer, error) {
			return timer.NewConsumer(timer.ConsumerDeps{Source: &fakeSource{}, Messages: r}, timer.ConsumerConfig{}, nil)
		},
		"no messages": func() (*timer.Consumer, error) {
			return timer.NewConsumer(timer.ConsumerDeps{Source: &fakeSource{}, Members: r}, timer.ConsumerConfig{}, nil)
		},
		"huge batch": func() (*timer.Consumer, error) {
			return timer.NewConsumer(timer.ConsumerDeps{Source: &fakeSource{}, Members: r, Messages: r}, timer.ConsumerConfig{FetchBatch: timer.MaxAckPending + 1}, nil)
		},
	}
	for name, build := range bad {
		if c, err := build(); !errors.Is(err, apperr.ErrInvalidArgument) || c != nil {
			t.Errorf("%s: NewConsumer = %v, %v", name, c, err)
		}
	}
	synctest.Test(t, func(t *testing.T) {
		rg := newConsumerRig(t, t.Context())
		synctest.Wait()
		if err := rg.c.Run(t.Context()); err == nil {
			t.Fatalf("second Run = nil, want an error")
		}
		rg.stop(t)
	})
	unstarted := built(timer.NewConsumer(timer.ConsumerDeps{Source: &fakeSource{}, Members: r, Messages: r}, timer.ConsumerConfig{}, nil))(t)
	if err := unstarted.Close(t.Context()); err != nil {
		t.Fatalf("Close before Run = %v", err)
	}
}
