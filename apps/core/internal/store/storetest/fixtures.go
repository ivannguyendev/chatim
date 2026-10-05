package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	roomA      uint64 = 7_340_000_001
	roomB      uint64 = 7_340_000_002
	mainThread uint64 = 0
	sideThread uint64 = 7
	tenant            = "acme"
)

var baseTime = time.UnixMilli(1_700_000_000_000).UTC()

func msg(room, thread, seq uint64) domain.Message {
	return domain.Message{
		Room:      room,
		Thread:    thread,
		Seq:       seq,
		Tenant:    tenant,
		From:      "alice",
		Kind:      domain.KindText,
		Text:      fmt.Sprintf("text %d/%d/%d", room, thread, seq),
		CID:       fmt.Sprintf("cid-%d-%d-%d", room, thread, seq),
		CreatedAt: baseTime,
	}
}

func span(room, thread, from, to uint64) []domain.Message {
	out := []domain.Message{}
	for seq := from; seq <= to; seq++ {
		out = append(out, msg(room, thread, seq))
	}
	return out
}

func seqRange(from, to uint64) []uint64 {
	out := []uint64{}
	for seq := from; seq <= to; seq++ {
		out = append(out, seq)
	}
	return out
}

func seqsOf(msgs []domain.Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.Seq
	}
	return out
}

func keysOf(msgs []domain.Message) []store.MsgKey {
	out := make([]store.MsgKey, len(msgs))
	for i, m := range msgs {
		out[i] = store.KeyOf(m)
	}
	return out
}

func cancelledContext(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func mustInsert(t *testing.T, s store.Messages, msgs []domain.Message) {
	t.Helper()
	want := slices.Repeat([]store.Outcome{store.Inserted}, len(msgs))
	assertOutcomes(t, s.Insert(t.Context(), msgs), want)
}

func assertOutcomes(t *testing.T, got []store.Result, want []store.Outcome) {
	t.Helper()
	outcomes := make([]store.Outcome, 0, len(got))
	for _, r := range got {
		outcomes = append(outcomes, r.Outcome)
	}
	if !slices.Equal(outcomes, want) {
		t.Fatalf("Insert outcomes = %v, want %v; results %+v", outcomes, want, got)
	}
	for i, r := range got {
		switch {
		case r.Outcome == store.Rejected && !errors.Is(r.Err, apperr.ErrInvalidArgument):
			t.Fatalf("result[%d] rejected with %v, want ErrInvalidArgument", i, r.Err)
		case r.Outcome != store.Rejected && r.Err != nil:
			t.Fatalf("result[%d] %v carries error %v, want nil", i, r.Outcome, r.Err)
		}
	}
}

func assertMessages(t *testing.T, got, want []domain.Message) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameMessage) {
		t.Fatalf("messages = %+v,\nwant %+v", got, want)
	}
}

func assertSeqs(t *testing.T, got []domain.Message, room, thread uint64, want []uint64) {
	t.Helper()
	for _, m := range got {
		if m.Room != room || m.Thread != thread {
			t.Fatalf("message %+v is outside timeline %d/%d", store.KeyOf(m), room, thread)
		}
	}
	if seqs := seqsOf(got); !slices.Equal(seqs, want) {
		t.Fatalf("seqs = %v, want %v", seqs, want)
	}
}

func assertErrorIs(t *testing.T, op string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s error = %v, want %v", op, err, want)
	}
}

func sameMessage(a, b domain.Message) bool {
	at, bt, ae, be := a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt
	a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return a == b && at.Equal(bt) && ae.Equal(be)
}
