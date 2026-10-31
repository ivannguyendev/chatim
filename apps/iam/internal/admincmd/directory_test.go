package admincmd_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/admincmd"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestSplitGroupsTrimsEachPart(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", []string{}},
		{"sales", []string{"sales"}},
		{" sales , hn:north", []string{"sales", "hn:north"}},
	}
	for _, c := range cases {
		got, err := admincmd.SplitGroupsForTest(c.raw)
		if err != nil || !slices.Equal(got, c.want) || got == nil {
			t.Fatalf("split(%q) = %v, %v; want %v", c.raw, got, err, c.want)
		}
	}
}

func TestSplitGroupsRefusesEmptyBadAndRepeatedParts(t *testing.T) {
	for _, raw := range []string{",", "sales,", "sales,,hn", " ", "bad id", "sales,sales", "sales, sales"} {
		if _, err := admincmd.SplitGroupsForTest(raw); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("split(%q) err = %v; want invalid argument", raw, err)
		}
	}
}
