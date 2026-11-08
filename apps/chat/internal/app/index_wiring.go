package app

import (
	"github.com/ivannguyendev/chatim/apps/chat/internal/config"
	"github.com/ivannguyendev/chatim/apps/chat/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/core/metrics"
	"github.com/ivannguyendev/chatim/pkg/core/mongoindex"
)

type indexProbes struct {
	health      *mongoindex.Health
	collections []string
}

func indexCatalog(cfg config.Config) mongoindex.Catalog {
	cat := mongostore.Indexes()
	if cfg.Mention.Enabled {
		cat = cat.With(mentionapp.Indexes())
	}
	if cfg.Media.Enabled {
		cat = cat.With(mediaapp.Indexes())
	}
	return cat
}

func indexSources(p indexProbes) []metrics.Source {
	const help = "1 while this collection lacks a declared index or keeps an outdated one; run /app indexes."
	out := make([]metrics.Source, 0, len(p.collections))
	for _, coll := range p.collections {
		out = append(out, metrics.Source{
			Name: "index_outdated", Help: help, Gauge: true, Labels: map[string]string{"collection": coll},
			Read: func() float64 { return flag(p.health.Outdated(coll)) },
		})
	}
	return out
}
