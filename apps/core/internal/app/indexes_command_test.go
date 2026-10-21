package app

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseIndexesArgsPicksOneMode(t *testing.T) {
	cases := []struct {
		args []string
		want IndexesMode
	}{
		{nil, IndexesDryRun},
		{[]string{"-dry-run"}, IndexesDryRun},
		{[]string{"-apply"}, IndexesApply},
		{[]string{"-drop-old"}, IndexesDropOld},
	}
	for _, c := range cases {
		got, err := parseIndexesArgs(c.args, &bytes.Buffer{})
		if err != nil || got.Mode != c.want {
			t.Errorf("parseIndexesArgs(%v) = %+v, %v; want mode %d", c.args, got, err, c.want)
		}
	}
	for _, args := range [][]string{{"-apply", "-drop-old"}, {"-dry-run", "-apply"}, {"-bogus"}, {"extra"}} {
		if _, err := parseIndexesArgs(args, &bytes.Buffer{}); !errors.Is(err, errIndexesUsage) {
			t.Errorf("parseIndexesArgs(%v) error = %v, want errIndexesUsage", args, err)
		}
	}
}

func TestIndexesRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"indexes", "-apply", "-drop-old"}, {"indexes", "extra"}} {
		if got := Main(args); got != 2 {
			t.Errorf("Main(%v) = %d, want 2", args, got)
		}
	}
}

func TestPrintDoneSaysNothingWhenTheStepFailed(t *testing.T) {
	var out bytes.Buffer
	printDone(&out, "dropped", nil, noneUnless(errIndexesUsage, "no outdated index"))
	printDone(&out, "created", []string{"members a_1"}, noneUnless(nil, "no missing index"))
	printDone(&out, "", nil, "indexes up to date")
	if got, want := out.String(), "created members a_1\nindexes up to date\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
