package publish

import (
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const failureLogEvery = time.Second

type failureLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	last time.Time
}

func (f *failureLog) record(msg, id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if now := time.Now(); now.Sub(f.last) >= failureLogEvery {
		f.last = now
		f.log.Warn(msg, "event", id, "err", err)
	}
}

func AsyncFailureHandler(log *slog.Logger, counters *Counters) jetstream.MsgErrHandler {
	if log == nil {
		log = slog.Default()
	}
	if counters == nil {
		counters = &Counters{}
	}
	f := &failureLog{log: log}
	return func(_ jetstream.JetStream, m *nats.Msg, err error) {
		counters.asyncFailed.Add(1)
		f.record("event publish failed; reconciliation must republish it", m.Header.Get(jetstream.MsgIDHeader), err)
	}
}
