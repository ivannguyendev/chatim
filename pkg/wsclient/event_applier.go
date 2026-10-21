package wsclient

import (
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const DefaultRecentIDs = 10_000

type Verdict uint8

const (
	VerdictDeliver Verdict = iota
	VerdictDuplicate
	VerdictStale
)

type Gap struct {
	RoomID     string
	ThreadRoot uint64
	From       uint64
	To         uint64
}

func (g Gap) Empty() bool { return g.From == 0 }

type Outcome struct {
	Verdict Verdict
	Gap     Gap
}

type ApplierStats struct {
	Delivered  uint64
	Duplicates uint64
	Stale      uint64
	Gaps       uint64
}

type timeline struct {
	room   string
	thread uint64
}

type timelineSeq struct {
	timeline
	seq uint64
}

type Applier struct {
	mu       sync.Mutex
	ids      *recentSet[string]
	seqs     *recentSet[timelineSeq]
	lastSeq  map[timeline]uint64
	versions map[docKey]uint64
	stats    ApplierStats
}

func NewApplier(recentIDs int) *Applier {
	if recentIDs <= 0 {
		recentIDs = DefaultRecentIDs
	}
	return &Applier{
		ids:      newRecentSet[string](recentIDs),
		seqs:     newRecentSet[timelineSeq](recentIDs),
		lastSeq:  make(map[timeline]uint64),
		versions: make(map[docKey]uint64),
	}
}

func (a *Applier) Apply(ev *chatimv1.Event) Outcome {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id := ev.GetId(); id != "" && !a.ids.add(id) {
		a.stats.Duplicates++
		return Outcome{Verdict: VerdictDuplicate}
	}
	version, versioned := versionOf(ev)
	if versioned && !version.recordOnly {
		if known, ok := a.versions[version.key]; ok && version.ver <= known {
			a.stats.Stale++
			return Outcome{Verdict: VerdictStale}
		}
	}
	var gap Gap
	if ev.GetMessageCreated() != nil {
		key := timelineSeq{timeline: timeline{room: ev.GetRoomId(), thread: ev.GetThreadRoot()}, seq: ev.GetSeq()}
		if !a.seqs.add(key) {
			a.stats.Duplicates++
			return Outcome{Verdict: VerdictDuplicate}
		}
		gap = a.advance(key)
	}
	if versioned {
		a.versions[version.key] = max(a.versions[version.key], version.ver)
	}
	a.stats.Delivered++
	return Outcome{Verdict: VerdictDeliver, Gap: gap}
}

func (a *Applier) MarkSeq(roomID string, threadRoot, seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	line := timeline{room: roomID, thread: threadRoot}
	a.lastSeq[line] = max(a.lastSeq[line], seq)
}

func (a *Applier) Stats() ApplierStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}

func (a *Applier) advance(key timelineSeq) Gap {
	last, known := a.lastSeq[key.timeline]
	if key.seq <= last {
		return Gap{}
	}
	a.lastSeq[key.timeline] = key.seq
	if !known || key.seq == last+1 {
		return Gap{}
	}
	a.stats.Gaps++
	return Gap{RoomID: key.room, ThreadRoot: key.thread, From: last + 1, To: key.seq - 1}
}
