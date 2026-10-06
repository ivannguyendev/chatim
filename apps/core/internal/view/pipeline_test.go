package view_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
)

func msg(seq uint64, from, cid string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: from, CID: cid}
}

func seqs(msgs []domain.Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.Seq
	}
	return out
}

func TestCollapseRetriedKeepsTheLowestSeqOfEachSend(t *testing.T) {
	page := []domain.Message{msg(9, "alice", "c-1"), msg(4, "alice", "c-1"), msg(5, "bob", "c-1"), msg(6, "alice", "c-2"), msg(7, "", ""), msg(8, "", "")}
	before := slices.Clone(page)
	got := view.CollapseRetried(view.Viewer{User: "bob"}, page)
	if want := []uint64{4, 5, 6, 7, 8}; !slices.Equal(seqs(got), want) {
		t.Fatalf("seqs = %v, want %v", seqs(got), want)
	}
	if !reflect.DeepEqual(page, before) {
		t.Fatalf("input page was modified")
	}
}

func TestPipelineRunsStepsInOrder(t *testing.T) {
	var order []string
	step := func(name string) view.Step {
		return func(_ view.Viewer, msgs []domain.Message) []domain.Message {
			order = append(order, name)
			return msgs[1:]
		}
	}
	got := view.New(step("a"), step("b")).Apply(view.Viewer{}, []domain.Message{msg(1, "x", "1"), msg(2, "x", "2"), msg(3, "x", "3")})
	if !slices.Equal(order, []string{"a", "b"}) || !slices.Equal(seqs(got), []uint64{3}) {
		t.Fatalf("order = %v, seqs = %v; want [a b], [3]", order, seqs(got))
	}
	if got := view.Default().Apply(view.Viewer{}, nil); len(got) != 0 {
		t.Fatalf("default pipeline on nil = %v, want empty", got)
	}
}
