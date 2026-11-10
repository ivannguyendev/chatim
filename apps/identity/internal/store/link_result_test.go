package store

import "testing"

func TestLinkResultNamesEachOutcome(t *testing.T) {
	for r, want := range map[LinkResult]string{
		LinkCreated: "created", LinkExisting: "existing", LinkHealed: "healed", LinkPending: "pending", 0: "unknown",
	} {
		if got := r.String(); got != want {
			t.Fatalf("LinkResult(%d).String() = %q; want %q", r, got, want)
		}
	}
}
