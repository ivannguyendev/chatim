package objectstore

import "sync/atomic"

type operation int

const (
	opStart operation = iota
	opComplete
	opAbort
	opHead
	opDelete
	opCheckBuckets
	opCopy
	operationCount
)

type result int

const (
	resultOK result = iota
	resultClient
	resultError
	resultCount
)

var (
	operationNames = [operationCount]string{"start", "complete", "abort", "head", "delete", "check_buckets", "copy"}
	resultNames    = [resultCount]string{"ok", "client", "error"}
)

type Stats struct {
	counts [operationCount][resultCount]atomic.Uint64
}

type Count struct {
	Operation, Result string
	Read              func() uint64
}

func (s *Stats) record(op operation, err error) {
	s.counts[op][resultOf(err)].Add(1)
}

func (s *Stats) Counts() []Count {
	out := make([]Count, 0, int(operationCount)*int(resultCount))
	for op := range operationCount {
		for r := range resultCount {
			c := &s.counts[op][r]
			out = append(out, Count{Operation: operationNames[op], Result: resultNames[r], Read: c.Load})
		}
	}
	return out
}
