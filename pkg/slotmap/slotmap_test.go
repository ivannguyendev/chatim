package slotmap

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestOfSpreadsRoomsEvenly(t *testing.T) {
	const n = 1_000_000
	rng := rand.New(rand.NewPCG(3, 4))
	sources := map[string]func(i int) uint64{
		"random":     func(int) uint64 { return rng.Uint64() >> 1 },
		"sequential": func(i int) uint64 { return uint64(i + 1) },
	}
	for name, next := range sources {
		counts := make([]int, Count)
		for i := range n {
			counts[Of(next(i))]++
		}
		mean := n / Count
		for s, c := range counts {
			if c < mean*8/10 || c > mean*12/10 {
				t.Fatalf("%s rooms: slot %d has %d rooms, mean %d", name, s, c, mean)
			}
		}
	}
}

func TestOfIsDeterministic(t *testing.T) {
	pinned := map[uint64]uint16{0: 0, 1: 812, 42: 460, 0x5f00aa11bb22cc33: 250, ^uint64(0): 801}
	for room, want := range pinned {
		if got := Of(room); got != want {
			t.Errorf("Of(%#x) = %d, want %d: slot assignment must never change", room, got, want)
		}
	}
}

func TestPreferredMovesOnlyTheNewCoreShare(t *testing.T) {
	before := []string{"core-a", "core-b", "core-c"}
	after := append(slices.Clone(before), "core-d")
	moved := 0
	for s := range uint16(Count) {
		was, now := Preferred(s, before), Preferred(s, after)
		if was == now {
			continue
		}
		moved++
		if now != "core-d" {
			t.Fatalf("slot %d moved %s -> %s, want moves only to core-d", s, was, now)
		}
	}
	if moved < Count*15/100 || moved > Count*35/100 {
		t.Fatalf("moved %d of %d slots, want about a quarter", moved, Count)
	}
}

func TestPreferredBalancesCores(t *testing.T) {
	cores := []string{"core-a", "core-b", "core-c"}
	share := map[string]int{}
	for s := range uint16(Count) {
		share[Preferred(s, cores)]++
	}
	for _, c := range cores {
		if share[c] < Count*25/100 || share[c] > Count*42/100 {
			t.Fatalf("core %s prefers %d of %d slots, want about a third", c, share[c], Count)
		}
	}
	if Preferred(0, nil) != "" {
		t.Fatal("Preferred with no cores must return empty string")
	}
}

func TestRedisKeys(t *testing.T) {
	if got := SlotKey(17); got != "chatim:slot:17" {
		t.Fatalf("SlotKey(17) = %q", got)
	}
	id, ok := CoreIDFromKey(CoreKey("core-a"))
	if !ok || id != "core-a" {
		t.Fatalf("CoreIDFromKey(CoreKey) = %q, %v", id, ok)
	}
	if _, ok := CoreIDFromKey("chatim:slot:1"); ok {
		t.Fatal("slot key must not parse as core key")
	}
}
