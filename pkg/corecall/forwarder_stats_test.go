package corecall_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/corecall"
)

func readCount(t *testing.T, fwd *corecall.Forwarder, cmd, code string) float64 {
	t.Helper()
	for _, s := range fwd.CommandSources() {
		if s.Labels["command"] == cmd && s.Labels["code"] == code {
			return s.Read()
		}
	}
	t.Fatalf("no counter for %s %s", cmd, code)
	return 0
}

func TestStatsCoverOnlyTheCallersCommands(t *testing.T) {
	h := newHarness(t, time.Second, nil)
	if got := len(h.fwd.CommandSources()); got != 34*17 {
		t.Fatalf("sources = %d", got)
	}
	route := func(corecall.Command, proto.Message) (corecall.Conn, error) { return h.conn, nil }
	fwd, err := corecall.New(corecall.Options{Methods: corecall.Realtime(), Deadline: time.Second, Route: route})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range fwd.CommandSources() {
		if s.Name != "commands_total" {
			t.Fatalf("source %+v", s)
		}
		names[s.Labels["command"]] = true
	}
	if len(names) != 12 || !names["SendMessage"] || names["GetHistory"] || len(fwd.CoreCallLatency()) != 12 {
		t.Fatalf("names = %v", names)
	}
	if _, outcome := fwd.Forward(t.Context(), testClaims, sendMethod, plainSend()); outcome.Message != "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := readCount(t, fwd, "SendMessage", "OK"); got != 1 {
		t.Fatalf("SendMessage OK = %v", got)
	}
	if got := fwd.CoreCallLatency()[0].Read().Count; got != 1 {
		t.Fatalf("SendMessage latency count = %d", got)
	}
}
