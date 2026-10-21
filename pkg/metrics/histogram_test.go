package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/metrics"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

func TestHandlerExposesHistogramsReadAtScrapeTime(t *testing.T) {
	current := metrics.Histogram{Bounds: []float64{0.01, 0.1}, Cumulative: []uint64{1, 3}, Count: 4, Sum: 2.5}
	read := func() metrics.Histogram { return current }
	h, err := metrics.Handler("websocket", []metrics.Source{
		{Name: "core_call_seconds", Help: "Core call latency.", Labels: map[string]string{"command": "GetHistory"}, Histogram: read},
		{Name: "core_call_seconds", Help: "Core call latency.", Labels: map[string]string{"command": "SendMessage"}, Histogram: func() metrics.Histogram {
			return metrics.Histogram{Bounds: []float64{0.01, 0.1}, Cumulative: []uint64{0, 0}}
		}},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	body := scrape(t, h)
	for _, want := range []string{
		`# TYPE chatim_websocket_core_call_seconds histogram`,
		`chatim_websocket_core_call_seconds_bucket{command="GetHistory",le="0.01"} 1`,
		`chatim_websocket_core_call_seconds_bucket{command="GetHistory",le="0.1"} 3`,
		`chatim_websocket_core_call_seconds_bucket{command="GetHistory",le="+Inf"} 4`,
		`chatim_websocket_core_call_seconds_sum{command="GetHistory"} 2.5`,
		`chatim_websocket_core_call_seconds_count{command="GetHistory"} 4`,
		`chatim_websocket_core_call_seconds_count{command="SendMessage"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
	current = metrics.Histogram{Bounds: []float64{0.01, 0.1}, Cumulative: []uint64{2, 5}, Count: 6, Sum: 3}
	if body := scrape(t, h); !strings.Contains(body, `chatim_websocket_core_call_seconds_count{command="GetHistory"} 6`) {
		t.Errorf("second scrape did not read the histogram again: %s", body)
	}
}

func TestHandlerReportsAMalformedHistogramAsAScrapeError(t *testing.T) {
	h, err := metrics.Handler("websocket", []metrics.Source{
		{Name: "core_call_seconds", Help: "Core call latency.", Histogram: func() metrics.Histogram {
			return metrics.Histogram{Bounds: []float64{0.01, 0.1}, Cumulative: []uint64{1}}
		}},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for bounds and counts of different lengths", rec.Code)
	}
}

func TestHandlerRejectsSourcesWithoutExactlyOneReader(t *testing.T) {
	both := metrics.Source{Name: "x", Help: "x", Read: func() float64 { return 0 }, Histogram: func() metrics.Histogram { return metrics.Histogram{} }}
	gauge := metrics.Source{Name: "y", Help: "y", Gauge: true, Histogram: func() metrics.Histogram { return metrics.Histogram{} }}
	for name, s := range map[string]metrics.Source{"none": {Name: "z", Help: "z"}, "both": both, "gauge histogram": gauge} {
		if _, err := metrics.Handler("websocket", []metrics.Source{s}); err == nil {
			t.Errorf("%s: Handler accepted the source", name)
		}
	}
}
