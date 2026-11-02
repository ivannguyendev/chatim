package mongostore

import (
	"testing"
	"time"
)

func TestOpenRejectsNonPositiveRevocationKeep(t *testing.T) {
	for _, keep := range []time.Duration{0, -time.Second} {
		if _, err := Open(Options{URI: "mongodb://db:27017", Database: "chatim_identity", RevocationKeep: keep}); err == nil {
			t.Fatalf("Open with RevocationKeep %v = nil; want an error", keep)
		}
	}
}
