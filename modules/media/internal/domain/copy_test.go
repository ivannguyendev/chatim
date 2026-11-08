package domain_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestCopyLimitsStayWithinS3Bounds(t *testing.T) {
	for name, tc := range map[string]struct {
		limits domain.CopyLimits
		ok     bool
	}{
		"defaults":         {domain.CopyLimits{SingleMax: 5 * gib, PartBytes: gib}, true},
		"smallest":         {domain.CopyLimits{SingleMax: 1, PartBytes: 5 * domain.MiB}, true},
		"largest part":     {domain.CopyLimits{SingleMax: 5 * gib, PartBytes: 5 * gib}, true},
		"zero single":      {domain.CopyLimits{SingleMax: 0, PartBytes: gib}, false},
		"single over 5GiB": {domain.CopyLimits{SingleMax: 5*gib + 1, PartBytes: gib}, false},
		"part under 5MiB":  {domain.CopyLimits{SingleMax: gib, PartBytes: 5*domain.MiB - 1}, false},
		"part over 5GiB":   {domain.CopyLimits{SingleMax: gib, PartBytes: 5*gib + 1}, false},
	} {
		if err := tc.limits.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok %v", name, err, tc.ok)
		}
	}
}

func TestCopyGoesWholeUpToTheSingleMax(t *testing.T) {
	l := domain.CopyLimits{SingleMax: 5 * domain.MiB, PartBytes: 5 * domain.MiB}
	if !l.Whole(5*domain.MiB) || l.Whole(5*domain.MiB+1) {
		t.Fatal("whole copy boundary")
	}
}

func TestCopyPartsCoverTheObjectInOrder(t *testing.T) {
	l := domain.CopyLimits{SingleMax: 5 * domain.MiB, PartBytes: 5 * domain.MiB}
	got, err := l.Parts(12 * domain.MiB)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ByteRange{{0, 5 * domain.MiB}, {5 * domain.MiB, 5 * domain.MiB}, {10 * domain.MiB, 2 * domain.MiB}}
	if !slices.Equal(got, want) {
		t.Fatalf("parts %v", got)
	}
	exact, err := l.Parts(10 * domain.MiB)
	if err != nil || len(exact) != 2 || exact[1].Length != 5*domain.MiB {
		t.Fatalf("exact multiple: %v %v", exact, err)
	}
}

func TestCopyPartsRefuseMoreThanS3Allows(t *testing.T) {
	l := domain.CopyLimits{SingleMax: 5 * domain.MiB, PartBytes: 5 * domain.MiB}
	if parts, err := l.Parts(10000 * 5 * domain.MiB); err != nil || len(parts) != 10000 {
		t.Fatalf("10000 parts: %d %v", len(parts), err)
	}
	if _, err := l.Parts(10000*5*domain.MiB + 1); err == nil {
		t.Fatal("10001 parts accepted")
	}
	if _, err := l.Parts(0); err == nil {
		t.Fatal("empty object accepted")
	}
}
