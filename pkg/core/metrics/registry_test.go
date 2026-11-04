package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

func TestHandlerExposesCountersAndGaugesFromSources(t *testing.T) {
	queueFull, refused := 3.0, 1.0
	h, err := metrics.Handler("core", []metrics.Source{
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return queueFull }},
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return refused }},
		{Name: "sweep_running", Help: "1 while this core runs the sweeper.", Gauge: true, Read: func() float64 { return 1 }},
		{Subsystem: "mention", Name: "lag_seconds", Help: "Mention lag.", Gauge: true, Read: func() float64 { return 2 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`chatim_core_publish_dropped_total{reason="queue_full"} 3`,
		`chatim_core_publish_dropped_total{reason="refused"} 1`,
		`# TYPE chatim_core_sweep_running gauge`,
		`chatim_core_sweep_running 1`,
		`chatim_mention_lag_seconds 2`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

func TestHandlerRejectsConflictingSources(t *testing.T) {
	_, err := metrics.Handler("core", []metrics.Source{
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
	})
	if err == nil {
		t.Fatal("Handler accepted two identical sources")
	}
}

func TestHandlerNamesSourcesAfterTheGivenSubsystem(t *testing.T) {
	h, err := metrics.Handler("websocket", []metrics.Source{
		{Name: "connections", Help: "Open connections.", Gauge: true, Read: func() float64 { return 7 }},
		{Subsystem: "mention", Name: "lag_seconds", Help: "Mention lag.", Gauge: true, Read: func() float64 { return 2 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{`chatim_websocket_connections 7`, `chatim_mention_lag_seconds 2`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
	if strings.Contains(string(body), "chatim_core_") {
		t.Errorf("metrics output names a source after core: %s", body)
	}
}
