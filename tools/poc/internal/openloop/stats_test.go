package openloop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

func TestStatsMeasureAckLatencyFromTheScheduledTime(t *testing.T) {
	var s Stats
	base := time.Unix(1_700_000_000, 0)
	for i := range 100 {
		shot := Shot{Index: i, Scheduled: base.Add(time.Duration(i) * time.Millisecond), Measured: true}
		s.Record(Result{Shot: shot, Done: shot.Scheduled.Add(time.Duration(i+1) * time.Millisecond), Code: codes.OK, Attempts: 1})
	}
	s.Record(Result{Shot: Shot{Scheduled: base}, Done: base.Add(time.Hour), Code: codes.OK, Attempts: 9})
	got := s.Sends()
	if got.Acked != 100 || got.Ack.Count != 100 || got.Ack.P50 != 50*time.Millisecond || got.Ack.Max != 100*time.Millisecond {
		t.Fatalf("sends = %+v, want 100 measured acks with p50 50ms and max 100ms", got)
	}
	if got.MaxAttempts != 1 || got.Retried != 0 || got.Attempts != 100 {
		t.Fatalf("attempts = %+v, want the warmup send ignored", got)
	}
}

func TestStatsCountFailuresByCodeAndRetries(t *testing.T) {
	var s Stats
	shot := Shot{Scheduled: time.Unix(0, 0), Measured: true}
	results := []Result{
		{Shot: shot, Code: codes.OK, Attempts: 1},
		{Shot: shot, Code: codes.OK, Attempts: 3},
		{Shot: shot, Code: codes.Unavailable, Attempts: 5},
		{Shot: shot, Code: codes.Unavailable, Attempts: 4},
		{Shot: shot, Code: codes.DeadlineExceeded, Attempts: 1},
	}
	var wg sync.WaitGroup
	for _, r := range results {
		wg.Go(func() { s.Record(r) })
	}
	wg.Wait()
	got := s.Sends()
	if got.Acked != 2 || got.Ack.Count != 2 || got.FailedTotal() != 3 {
		t.Fatalf("sends = %+v, want 2 acked and 3 failed", got)
	}
	if got.Failed[codes.Unavailable] != 2 || got.Failed[codes.DeadlineExceeded] != 1 {
		t.Fatalf("failed by code = %v", got.Failed)
	}
	if got.Retried != 3 || got.MaxAttempts != 5 || got.Attempts != 14 {
		t.Fatalf("retries = %+v, want 3 retried sends, max 5, 14 attempts", got)
	}
}

func TestAStallShowsInAckLatencyOfEveryShotScheduledDuringIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Stats
		start := time.Now()
		stallFrom, stallUntil := start.Add(10*time.Millisecond), start.Add(60*time.Millisecond)
		p := Plan{Rate: 1000, Duration: 100 * time.Millisecond, MaxInflight: 1000}
		Run(t.Context(), p, func(_ context.Context, shot Shot) {
			if now := time.Now(); !now.Before(stallFrom) && now.Before(stallUntil) {
				time.Sleep(stallUntil.Sub(now))
			}
			s.Record(Result{Shot: shot, Done: time.Now(), Code: codes.OK, Attempts: 1})
		})
		got := s.Sends().Ack
		want := latency.Summary{Count: 100, P50: 0, P95: 45 * time.Millisecond, P99: 49 * time.Millisecond, P999: 50 * time.Millisecond, Max: 50 * time.Millisecond}
		if got != want {
			t.Fatalf("ack latency = %+v, want %+v", got, want)
		}
	})
}

func TestReportPrintsRatesSheddingRetriesAndPercentiles(t *testing.T) {
	r := Report{
		Plan:   Plan{Rate: 1000, Warmup: time.Second, Duration: 2 * time.Second, MaxInflight: 64},
		Counts: Counts{All: Window{Due: 3000, Issued: 2990, Shed: 10}, Measured: Window{Due: 2000, Issued: 1996, Shed: 4}, Lag: latency.Summary{Count: 2000, Max: 3 * time.Millisecond}},
		Sends:  Sends{Acked: 1990, Failed: map[codes.Code]int{codes.Unavailable: 4, codes.DeadlineExceeded: 2}, Attempts: 2010, Retried: 12, MaxAttempts: 3},
	}
	var b strings.Builder
	r.Write(&b)
	out := b.String()
	for _, want := range []string{
		"target=1000/s sent=998/s acked=995/s",
		"due=2000 sent=1996 acked=1990 failed=6 shed_by_client=4",
		"failed by code: DeadlineExceeded=2 Unavailable=4",
		"retried sends=12 attempts=2010 max attempts=3",
		"with warmup: due=3000 sent=2990 shed_by_client=10",
		"pacer lag (issue minus scheduled time): n=2000 p50=0s",
		"ack latency from scheduled time: n=0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}
}
