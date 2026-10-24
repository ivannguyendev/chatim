package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/openloop"
	"github.com/ivannguyendev/chatim/tools/poc/internal/wsload"
)

type report struct {
	Scenario        string         `json:"scenario"`
	Run             string         `json:"run"`
	Finished        time.Time      `json:"finished"`
	URLs            []string       `json:"ws"`
	Gateway         string         `json:"gateway"`
	Tenant          string         `json:"tenant"`
	Conns           int            `json:"conns"`
	Groups          int            `json:"groups"`
	MembersPerGroup int            `json:"members_per_group"`
	Rate            int            `json:"rate"`
	ConnectRate     int            `json:"connect_rate"`
	DurationSeconds float64        `json:"duration_seconds"`
	SetupSeconds    float64        `json:"setup_seconds"`
	Connect         connectReport  `json:"connect"`
	Send            *sendReport    `json:"send,omitempty"`
	Kill            *killReport    `json:"kill,omitempty"`
	Frames          frameReport    `json:"frames"`
	Errors          map[string]int `json:"errors"`
}

type latencyReport struct {
	Count uint64  `json:"count"`
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

type connectReport struct {
	Ready            int64         `json:"ready"`
	Failed           int64         `json:"failed"`
	RetryLater       int64         `json:"retry_later"`
	LastReadySeconds float64       `json:"last_ready_seconds"`
	ElapsedSeconds   float64       `json:"elapsed_seconds"`
	Time             latencyReport `json:"time"`
}

type sendReport struct {
	Due       int            `json:"due"`
	Shed      int            `json:"shed"`
	Skipped   int64          `json:"skipped_not_connected"`
	Sent      int64          `json:"sent"`
	Acked     int            `json:"acked"`
	Errors    int            `json:"errors"`
	Failed    map[string]int `json:"failed"`
	AckP50Ms  float64        `json:"ack_p50_ms"`
	AckP99Ms  float64        `json:"ack_p99_ms"`
	Delivered int64          `json:"delivered"`
	Expected  int64          `json:"expected"`
	Duplicate int64          `json:"duplicate"`
	Latency   latencyReport  `json:"latency"`
}

type killReport struct {
	Dropped         int64   `json:"dropped"`
	Reconnected     int64   `json:"reconnected"`
	NotReconnected  int64   `json:"not_reconnected"`
	RecoverySeconds float64 `json:"recovery_seconds"`
	RetryLater      int64   `json:"retry_later"`
}

type frameReport struct {
	RetryLater int64 `json:"retry_later"`
	LoadFailed int64 `json:"load_failed"`
	Resync     int64 `json:"resync"`
}

func (l *load) sendReport(c openloop.Counts) *sendReport {
	s := l.sends.Sends()
	skipped := l.skipped.Load()
	failed := map[string]int{}
	for code, n := range s.Failed {
		failed[code.String()] = n
	}
	return &sendReport{
		Due: c.All.Due, Shed: c.All.Shed, Skipped: skipped, Sent: int64(c.All.Issued) - skipped,
		Acked: s.Acked, Errors: s.FailedTotal(), Failed: failed,
		AckP50Ms: ms(s.Ack.P50), AckP99Ms: ms(s.Ack.P99),
		Delivered: l.delivered.Load(), Expected: l.expected.Load(), Duplicate: l.duplicates.Load(),
		Latency: latencyOf(l.delivery.Summary()),
	}
}

func (l *load) writeSummary() error {
	c := l.cfg
	r := &l.report
	r.Scenario, r.Run, r.Finished = c.scenario, l.run, time.Now()
	r.URLs, r.Gateway, r.Tenant = c.urls, c.gateway, c.tenant
	r.Conns, r.Groups, r.MembersPerGroup = c.conns, c.groups, l.layout.MembersOf(0)
	r.Rate, r.ConnectRate, r.DurationSeconds = c.rate, c.connectRate, seconds(c.duration)
	r.Frames = frameReport{RetryLater: l.retryLater.Load(), LoadFailed: l.loadFailed.Load(), Resync: l.resyncs.Load()}
	r.Errors = l.errs.snapshot()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.out, "summary.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	var text strings.Builder
	r.writeText(&text)
	if err := os.WriteFile(filepath.Join(c.out, "summary.txt"), []byte(text.String()), 0o600); err != nil {
		return err
	}
	fmt.Print(text.String())
	return nil
}

func (r *report) writeText(w io.Writer) {
	fmt.Fprintf(w, "wsload %s run=%s conns=%d groups=%d members=%d rate=%d/s connect-rate=%d/s duration=%vs ws=%s\n",
		r.Scenario, r.Run, r.Conns, r.Groups, r.MembersPerGroup, r.Rate, r.ConnectRate, r.DurationSeconds, strings.Join(r.URLs, ","))
	fmt.Fprintf(w, "setup: %.2fs\n", r.SetupSeconds)
	cr := r.Connect
	fmt.Fprintf(w, "connect: ready=%d failed=%d retry_later=%d last_ready=%.2fs elapsed=%.2fs time %s\n",
		cr.Ready, cr.Failed, cr.RetryLater, cr.LastReadySeconds, cr.ElapsedSeconds, cr.Time)
	if s := r.Send; s != nil {
		fmt.Fprintf(w, "send: due=%d shed=%d skipped=%d sent=%d acked=%d errors=%d %v ack p50=%.1fms p99=%.1fms\n",
			s.Due, s.Shed, s.Skipped, s.Sent, s.Acked, s.Errors, s.Failed, s.AckP50Ms, s.AckP99Ms)
		fmt.Fprintf(w, "delivery: delivered=%d expected=%d duplicate=%d latency %s\n", s.Delivered, s.Expected, s.Duplicate, s.Latency)
	}
	if k := r.Kill; k != nil {
		fmt.Fprintf(w, "kill: dropped=%d reconnected=%d not_reconnected=%d recovery=%.2fs retry_later=%d\n",
			k.Dropped, k.Reconnected, k.NotReconnected, k.RecoverySeconds, k.RetryLater)
	}
	fmt.Fprintf(w, "frames: retry_later=%d load_failed=%d resync=%d\n", r.Frames.RetryLater, r.Frames.LoadFailed, r.Frames.Resync)
	for _, k := range slices.Sorted(maps.Keys(r.Errors)) {
		fmt.Fprintf(w, "error %dx %s\n", r.Errors[k], k)
	}
}

func (l latencyReport) String() string {
	return fmt.Sprintf("n=%d p50=%.1fms p90=%.1fms p99=%.1fms max=%.1fms", l.Count, l.P50Ms, l.P90Ms, l.P99Ms, l.MaxMs)
}

func latencyOf(s wsload.Summary) latencyReport {
	return latencyReport{Count: s.Count, P50Ms: ms(s.P50), P90Ms: ms(s.P90), P99Ms: ms(s.P99), MaxMs: ms(s.Max)}
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
