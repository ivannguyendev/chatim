package openloop

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type Live struct {
	mu       sync.Mutex
	warm     int
	lag      latency.Recorder
	seen     map[int]struct{}
	dups     int
	expected int
}

type LiveSummary struct {
	Expected, Received, Duplicates int
	Lag                            latency.Summary
}

func CID(run string, shot int) string { return run + "-" + strconv.Itoa(shot) }

func ShotOf(run, cid string) (int, bool) {
	rest, ok := strings.CutPrefix(cid, run+"-")
	if !ok || rest == "" || rest[0] < '0' || rest[0] > '9' {
		return 0, false
	}
	i, err := strconv.Atoi(rest)
	return i, err == nil
}

func NewLive(warmupShots int) *Live {
	return &Live{warm: warmupShots, seen: map[int]struct{}{}}
}

func (l *Live) Expect() {
	l.mu.Lock()
	l.expected++
	l.mu.Unlock()
}

func (l *Live) Observe(shot int, stamped, received time.Time) {
	if shot < l.warm {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, dup := l.seen[shot]; dup {
		l.dups++
		return
	}
	l.seen[shot] = struct{}{}
	l.lag.Add(received.Sub(stamped))
}

func (l *Live) Complete() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen) >= l.expected
}

func (l *Live) Summary() LiveSummary {
	l.mu.Lock()
	defer l.mu.Unlock()
	return LiveSummary{Expected: l.expected, Received: len(l.seen), Duplicates: l.dups, Lag: l.lag.Summary()}
}
