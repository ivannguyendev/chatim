package mongostore

import "testing"

func TestOplogWindowReadsTheReplicaSetLog(t *testing.T) {
	client := itClient(t)
	window, err := OplogWindow(t.Context(), client)
	if err != nil {
		t.Fatalf("OplogWindow: %v", err)
	}
	if window < 0 {
		t.Fatalf("window = %v, want >= 0", window)
	}
}
