package command_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/command"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func readCount(t *testing.T, sources []metrics.Source, cmd, code string) float64 {
	t.Helper()
	for _, s := range sources {
		if s.Labels["command"] == cmd && s.Labels["code"] == code {
			return s.Read()
		}
	}
	t.Fatalf("no counter for %s %s", cmd, code)
	return 0
}

func latencyOf(t *testing.T, latencies []command.CommandLatency, cmd string) metrics.Histogram {
	t.Helper()
	for _, l := range latencies {
		if l.Command == cmd {
			return l.Read()
		}
	}
	t.Fatalf("no latency for %s", cmd)
	return metrics.Histogram{}
}

func refusedSend() *chatimv1.ClientRequest {
	return sendRequest(&chatimv1.SendMessageRequest{RoomId: "42", Cid: "c1", Meta: []byte("x")})
}

func TestCommandCountersArePrebuiltForEveryPair(t *testing.T) {
	h := newHarness(t, time.Second)
	sources := h.exec.CommandSources()
	if len(sources) != 12*17 {
		t.Fatalf("sources = %d, want %d", len(sources), 12*17)
	}
	seen := map[string]bool{}
	for _, s := range sources {
		key := s.Labels["command"] + "/" + s.Labels["code"]
		if s.Name != "commands_total" || s.Gauge || len(s.Labels) != 2 || seen[key] {
			t.Fatalf("bad source %+v", s)
		}
		seen[key] = true
	}
	h.exec.Execute(t.Context(), testClaims, plainSend())
	h.exec.Execute(t.Context(), testClaims, refusedSend())
	if got := readCount(t, sources, "SendMessage", "OK"); got != 1 {
		t.Fatalf("SendMessage OK = %v", got)
	}
	if got := readCount(t, sources, "SendMessage", "InvalidArgument"); got != 1 {
		t.Fatalf("SendMessage InvalidArgument = %v", got)
	}
	if got := readCount(t, sources, "EditMessage", "OK"); got != 0 {
		t.Fatalf("EditMessage OK = %v", got)
	}
	for _, s := range sources {
		if s.Labels["command"] == "GetHistory" {
			t.Fatal("GetHistory is still counted on the websocket")
		}
	}
}

func TestCoreCallLatencyCountsOnlyCoreCalls(t *testing.T) {
	h := newHarness(t, time.Second)
	latencies := h.exec.CoreCallLatency()
	if len(latencies) != 12 {
		t.Fatalf("latencies = %d", len(latencies))
	}
	h.exec.Execute(t.Context(), testClaims, plainSend())
	h.exec.Execute(t.Context(), testClaims, refusedSend())
	hist := latencyOf(t, latencies, "SendMessage")
	if hist.Count != 1 || hist.Sum < 0 || len(hist.Cumulative) != len(hist.Bounds) || len(hist.Bounds) == 0 {
		t.Fatalf("hist = %+v", hist)
	}
	for i := 1; i < len(hist.Bounds); i++ {
		if hist.Bounds[i] <= hist.Bounds[i-1] || hist.Cumulative[i] < hist.Cumulative[i-1] {
			t.Fatalf("hist not cumulative: %+v", hist)
		}
	}
	if last := hist.Cumulative[len(hist.Cumulative)-1]; last > hist.Count {
		t.Fatalf("hist = %+v", hist)
	}
	if other := latencyOf(t, latencies, "EditMessage"); other.Count != 0 {
		t.Fatalf("EditMessage hist = %+v", other)
	}
}
