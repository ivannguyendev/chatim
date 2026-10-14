package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type topCount struct {
	Count int64 `bson:"count"`
}

type topEntry struct {
	Queries  topCount `bson:"queries"`
	GetMore  topCount `bson:"getmore"`
	Commands topCount `bson:"commands"`
}

type nsReads map[string]int64

func (p *mongoProbe) top(ctx context.Context) (nsReads, error) {
	var out struct {
		Totals map[string]bson.RawValue `bson:"totals"`
	}
	if err := p.client.Database("admin").RunCommand(ctx, bson.D{{Key: "top", Value: 1}}).Decode(&out); err != nil {
		return nil, fmt.Errorf("top: %w", err)
	}
	reads := nsReads{}
	for ns, raw := range out.Totals {
		coll, ok := strings.CutPrefix(ns, p.db+".")
		if !ok || raw.Type != bson.TypeEmbeddedDocument {
			continue
		}
		var e topEntry
		if err := raw.Unmarshal(&e); err != nil {
			return nil, fmt.Errorf("top entry %s: %w", ns, err)
		}
		reads[coll] = e.Queries.Count + e.GetMore.Count + e.Commands.Count
	}
	return reads, nil
}

func (r nsReads) minus(b nsReads) nsReads {
	out := nsReads{}
	for k, v := range r {
		if d := v - b[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

func (r nsReads) total() int64 {
	var n int64
	for _, v := range r {
		n += v
	}
	return n
}

func (r nsReads) add(d nsReads) {
	for k, v := range d {
		r[k] += v
	}
}

func (r nsReads) per(n int) string {
	if len(r) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(r))
	for _, k := range slices.Sorted(maps.Keys(r)) {
		parts = append(parts, fmt.Sprintf("%s=%.1f", k, float64(r[k])/float64(max(n, 1))))
	}
	return strings.Join(parts, " ")
}
