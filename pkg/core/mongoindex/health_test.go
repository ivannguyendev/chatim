package mongoindex

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestHealthFlagsAndWarnsOnlyStaleCollections(t *testing.T) {
	var logs bytes.Buffer
	h := NewHealth(slog.New(slog.NewTextHandler(&logs, nil)))
	h.Report(t.Context(), Report{Collection: "rooms"})
	h.Report(t.Context(), Report{Collection: "members", Missing: []mongo.IndexModel{{Keys: bson.D{{Key: "ver", Value: 1}}}}})
	if h.Outdated("rooms") || !h.Outdated("members") || h.Outdated("hidden") {
		t.Fatalf("outdated rooms/members/hidden = %v/%v/%v, want false/true/false", h.Outdated("rooms"), h.Outdated("members"), h.Outdated("hidden"))
	}
	out := logs.String()
	if strings.Count(out, OutdatedWarning) != 1 || !strings.Contains(out, "level=WARN") || !strings.Contains(out, "collection=members") || !strings.Contains(out, "ver_1") {
		t.Fatalf("logs = %q, want one warning naming members and ver_1", out)
	}
	h.Report(t.Context(), Report{Collection: "members"})
	if h.Outdated("members") {
		t.Fatal("members still outdated after a clean report")
	}
}
