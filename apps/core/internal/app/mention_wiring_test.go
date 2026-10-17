package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/platform/metrics"
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
)

type meetingDrainer struct {
	idle
	arrived chan struct{}
	other   chan struct{}
}

func (m meetingDrainer) Close(ctx context.Context) error {
	close(m.arrived)
	select {
	case <-m.other:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestShutdownDrainsProjectionsAndMentionTogether(t *testing.T) {
	order := &stopOrder{}
	a := stopNode(order)
	projections, mention := make(chan struct{}), make(chan struct{})
	a.projections = meetingDrainer{arrived: projections, other: mention}
	a.mention = meetingDrainer{arrived: mention, other: projections}
	if err := serveUntilStopped(t, a); err != nil {
		t.Fatalf("serve = %v, want both drains to meet within their phase", err)
	}
}

func TestShutdownWithoutMentionSkipsItsPhase(t *testing.T) {
	order := &stopOrder{}
	a := stopNode(order)
	a.mention = nil
	if err := serveUntilStopped(t, a); err != nil {
		t.Fatalf("serve = %v, want a clean stop", err)
	}
	for _, name := range order.names() {
		if name == "mention" {
			t.Fatalf("drain order %v ran a mention phase without the app", order.names())
		}
	}
}

func TestAttachWithoutMentionKeepsTheCoreWatches(t *testing.T) {
	a := &node{}
	watches := liveWatchers{&fakeWatch{}, &fakeWatch{}}
	if probes := a.attachMention(nil, watches); probes != nil || a.mention != nil || len(a.setups) != 0 {
		t.Fatalf("attach(nil) = %v, mention %v, setups %v; want nothing attached", probes, a.mention, a.setups)
	}
	if got, ok := a.watches.(liveWatchers); !ok || len(got) != 2 {
		t.Fatalf("watches = %v, want the two core watches", a.watches)
	}
}

func TestMentionProbesAreExportedAsChatimMention(t *testing.T) {
	probes := []mentionapp.Probe{
		{Name: "lag_seconds", Help: "lag", Gauge: true, Read: func() float64 { return 3 }},
		{Name: "dropped_total", Help: "dropped", Read: func() float64 { return 4 }},
		{Name: "notify_skipped_total", Help: "skipped", Labels: map[string]string{"reason": "stale"}, Read: func() float64 { return 5 }},
	}
	p := fakeProbes()
	p.mention = probes
	h, err := metrics.Handler(metricSources(p))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"chatim_mention_lag_seconds 3", "chatim_mention_dropped_total 4", `chatim_mention_notify_skipped_total{reason="stale"} 5`, "# TYPE chatim_mention_lag_seconds gauge"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %q", want)
		}
	}
}
