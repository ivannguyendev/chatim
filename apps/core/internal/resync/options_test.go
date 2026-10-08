package resync_test

import (
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var rangeArgs = []string{"-from", "2026-10-01T10:00:00Z", "-to", "2026-10-01T18:00:00+07:00"}

func TestParseArgsReadsEveryFlag(t *testing.T) {
	got, err := resync.ParseArgs(slices.Concat(rangeArgs, []string{"-tenant", "acme", "-room", "7340000001", "-rate", "50", "-dry-run"}), io.Discard)
	want := resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Room: 7_340_000_001, Rate: 50, DryRun: true}
	if err != nil || !got.From.Equal(want.From) || !got.To.Equal(want.To) {
		t.Fatalf("ParseArgs = %+v, %v; want %+v", got, err, want)
	}
	got.From, got.To = want.From, want.To
	if got != want {
		t.Fatalf("ParseArgs = %+v, want %+v", got, want)
	}
	if def, err := resync.ParseArgs(rangeArgs, io.Discard); err != nil || def.Rate != resync.DefaultRate || def.Room != 0 || def.DryRun {
		t.Fatalf("ParseArgs(range only) = %+v, %v; want rate %d, every room, publishing", def, err, resync.DefaultRate)
	}
}

func TestParseArgsRejectsBadInput(t *testing.T) {
	tests := map[string][]string{
		"no flags":         nil,
		"no end":           {"-from", "2026-10-01T10:00:00Z"},
		"end before start": {"-from", "2026-10-01T11:00:00Z", "-to", "2026-10-01T10:00:00Z"},
		"zero rate":        slices.Concat(rangeArgs, []string{"-rate", "0"}),
		"rate above max":   slices.Concat(rangeArgs, []string{"-rate", "10001"}),
		"bad room":         slices.Concat(rangeArgs, []string{"-room", "abc"}),
		"extra argument":   slices.Concat(rangeArgs, []string{"extra"}),
		"unknown flag":     slices.Concat(rangeArgs, []string{"-bogus"}),
	}
	for name, args := range tests {
		if _, err := resync.ParseArgs(args, io.Discard); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ParseArgs(%v) = %v, want ErrInvalidArgument", name, args, err)
		}
	}
}
