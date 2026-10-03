package main

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func printStorage(ctx context.Context, coll *mongo.Collection) error {
	cur, err := coll.Aggregate(ctx, mongo.Pipeline{{{Key: "$collStats", Value: bson.D{{Key: "storageStats", Value: bson.D{}}}}}})
	if err != nil {
		return fmt.Errorf("collStats: %w", err)
	}
	var out []struct {
		Storage struct {
			Count       int64 `bson:"count"`
			Size        int64 `bson:"size"`
			StorageSize int64 `bson:"storageSize"`
		} `bson:"storageStats"`
	}
	if err := cur.All(ctx, &out); err != nil {
		return fmt.Errorf("decode collStats: %w", err)
	}
	if len(out) == 0 {
		return fmt.Errorf("collStats returned no rows")
	}
	s := out[0].Storage
	if s.Count == 0 || s.StorageSize == 0 {
		fmt.Println("collection is empty")
		return nil
	}
	perDoc := float64(s.StorageSize) / float64(s.Count)
	fmt.Printf("docs=%d logical=%.1fMB on-disk=%.1fMB ratio=%.2fx logical/doc=%.0fB disk/doc=%.0fB\n",
		s.Count, mb(s.Size), mb(s.StorageSize), float64(s.Size)/float64(s.StorageSize), float64(s.Size)/float64(s.Count), perDoc)
	for _, n := range []float64{5e9, 20e9} {
		fmt.Printf("projected on-disk for %.0f billion messages: %.2f TB (before oplog, indexes, replicas)\n", n/1e9, n*perDoc/1e12)
	}
	return nil
}

func mb(b int64) float64 { return float64(b) / (1 << 20) }

type planNode struct {
	Stage      string    `bson:"stage"`
	InputStage *planNode `bson:"inputStage"`
	QueryPlan  *planNode `bson:"queryPlan"`
}

func (n *planNode) stages() []string {
	if n == nil {
		return nil
	}
	var out []string
	if n.Stage != "" {
		out = append(out, n.Stage)
	}
	out = append(out, n.QueryPlan.stages()...)
	return append(out, n.InputStage.stages()...)
}

func explain(ctx context.Context, coll *mongo.Collection, filter, sort bson.D, limit int64) ([]string, error) {
	var res struct {
		QueryPlanner struct {
			WinningPlan planNode `bson:"winningPlan"`
		} `bson:"queryPlanner"`
	}
	cmd := bson.D{
		{Key: "explain", Value: bson.D{{Key: "find", Value: coll.Name()}, {Key: "filter", Value: filter}, {Key: "sort", Value: sort}, {Key: "limit", Value: limit}}},
		{Key: "verbosity", Value: "queryPlanner"},
	}
	if err := coll.Database().RunCommand(ctx, cmd).Decode(&res); err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	return res.QueryPlanner.WinningPlan.stages(), nil
}
