package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	"github.com/ivannguyendev/chatim/pkg/mongoindex"
)

func TestIndexCatalogAddsMentionCollectionsOnlyWhenEnabled(t *testing.T) {
	var cfg config.Config
	core := indexCatalog(cfg).Names()
	if slices.Contains(core, "mentions") || !slices.Contains(core, "members") {
		t.Fatalf("catalog without mention = %v, want core collections only", core)
	}
	cfg.Mention.Enabled = true
	if got := indexCatalog(cfg).Names(); !slices.Contains(got, "mentions") || !slices.Contains(got, "mention_user_groups") || len(got) != len(core)+4 {
		t.Fatalf("catalog with mention = %v, want core plus the four mention collections", got)
	}
}

func TestIndexOutdatedGaugeFollowsTheBootstrapReports(t *testing.T) {
	health := mongoindex.NewHealth(quiet)
	health.Report(t.Context(), mongoindex.Report{Collection: "members", Missing: []mongo.IndexModel{{Keys: bson.D{{Key: "ver", Value: 1}}}}})
	health.Report(t.Context(), mongoindex.Report{Collection: "rooms"})
	p := fakeProbes()
	p.indexes = indexProbes{health: health, collections: []string{"members", "rooms"}}
	h, err := metrics.Handler(metricsSubsystem, metricSources(p))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{`chatim_core_index_outdated{collection="members"} 1`, `chatim_core_index_outdated{collection="rooms"} 0`, "# TYPE chatim_core_index_outdated gauge"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics lack %q", want)
		}
	}
}
