package projection_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
)

type fakeHandler struct {
	name  string
	keep  func(projection.Event) bool
	mu    sync.Mutex
	calls [][]uint64
	fails map[uint64]error
}

func keepAll(projection.Event) bool { return true }

func newFakeHandler(name string, keep func(projection.Event) bool) *fakeHandler {
	return &fakeHandler{name: name, keep: keep, fails: make(map[uint64]error)}
}

func (h *fakeHandler) Name() string { return h.name }

func (h *fakeHandler) Keeps(ev projection.Event) bool { return h.keep(ev) }

func (h *fakeHandler) Handle(_ context.Context, evs []projection.Event) []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	seqs := make([]uint64, len(evs))
	errs := make([]error, len(evs))
	for i, ev := range evs {
		seqs[i] = ev.Proto.GetSeq()
		errs[i] = h.fails[seqs[i]]
	}
	h.calls = append(h.calls, seqs)
	return errs
}

func (h *fakeHandler) failOn(seq uint64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		delete(h.fails, seq)
		return
	}
	h.fails[seq] = err
}

func (h *fakeHandler) handled() [][]uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

type settlement struct {
	acks  int
	terms int
	naks  []time.Duration
}

func settlements(ds []*fakeDelivery) []settlement {
	out := make([]settlement, len(ds))
	for i, d := range ds {
		acks, terms, naks := d.settled()
		out[i] = settlement{acks: acks, terms: terms, naks: naks}
	}
	return out
}

func acked(i, n int) []settlement {
	out := make([]settlement, n)
	out[i].acks = 1
	return out
}

func sameSettlements(got, want []settlement) bool {
	return slices.EqualFunc(got, want, func(a, b settlement) bool {
		return a.acks == b.acks && a.terms == b.terms && slices.Equal(a.naks, b.naks)
	})
}
