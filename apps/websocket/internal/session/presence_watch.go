package session

import (
	"sync"
	"sync/atomic"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type presenceDrop int

const (
	presenceDropSendFull presenceDrop = iota
	presenceDropBusy
	presenceDropCount
)

var presenceDropLabels = [presenceDropCount]string{"send_full", "busy"}

type presenceStats struct {
	answers   atomic.Uint64
	dropped   [presenceDropCount]atomic.Uint64
	truncated atomic.Uint64
	watching  atomic.Int64
}

func (s *presenceStats) sources() []metrics.Source {
	out := []metrics.Source{
		{Name: "presence_answers_total", Help: "Presence frames queued to watching connections.", Read: counterOf(&s.answers)},
		{Name: "presence_watch_truncated_total", Help: "watch_presence frames cut to WS_PRESENCE_WATCH_MAX users.", Read: counterOf(&s.truncated)},
		{Name: "presence_watching_connections", Help: "Connections with a non-empty presence ask list.", Gauge: true, Read: gaugeOf(&s.watching)},
	}
	for reason := range presenceDropCount {
		out = append(out, metrics.Source{
			Name: "presence_answer_dropped_total", Help: "Presence answers not sent, by reason.",
			Labels: map[string]string{"reason": presenceDropLabels[reason]}, Read: counterOf(&s.dropped[reason]),
		})
	}
	return out
}

type presenceWatch struct {
	stats   *presenceStats
	busy    atomic.Bool
	mu      sync.Mutex
	users   []string
	ver     uint64
	dropped bool
}

func newPresenceWatch(stats *presenceStats) *presenceWatch { return &presenceWatch{stats: stats} }

func (w *presenceWatch) replace(users []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dropped {
		return
	}
	switch {
	case len(w.users) == 0 && len(users) > 0:
		w.stats.watching.Add(1)
	case len(w.users) > 0 && len(users) == 0:
		w.stats.watching.Add(-1)
	}
	w.users = users
	w.ver++
}

func (w *presenceWatch) list() ([]string, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.users, w.ver
}

func (w *presenceWatch) current(ver uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.dropped && w.ver == ver
}

func (w *presenceWatch) drop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.dropped && len(w.users) > 0 {
		w.stats.watching.Add(-1)
	}
	w.dropped, w.users = true, nil
	w.ver++
}

func askList(users []string, limit int) ([]string, bool) {
	seen := make(map[string]struct{}, min(len(users), limit))
	list := make([]string, 0, min(len(users), limit))
	for _, id := range users {
		if ids.ValidUser(id) != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if len(list) == limit {
			return list, true
		}
		seen[id] = struct{}{}
		list = append(list, id)
	}
	return list, false
}

func (c *connection) currentWatch() *presenceWatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watch
}

func (c *connection) watchPresence(users []string) {
	w := c.currentWatch()
	if w == nil {
		return
	}
	list, truncated := askList(users, c.server.opts.PresenceWatchMax)
	if truncated {
		c.server.presenceStats.truncated.Add(1)
	}
	w.replace(list)
	c.answerPresence()
}

func (c *connection) answerPresence() {
	w := c.currentWatch()
	if w == nil {
		return
	}
	users, ver := w.list()
	if len(users) == 0 {
		return
	}
	if !w.busy.CompareAndSwap(false, true) {
		c.server.presenceStats.dropped[presenceDropBusy].Add(1)
		return
	}
	tenant := c.currentClaims().Tenant
	c.work.Go(func() {
		defer w.busy.Store(false)
		c.answer(w, ver, tenant, users)
	})
}

func (c *connection) answer(w *presenceWatch, ver uint64, tenant string, users []string) {
	defer c.recoverPanic()
	entries, err := c.server.presence.Answer(c.ctx, tenant, users)
	if err != nil || c.ctx.Err() != nil {
		return
	}
	payload, err := proto.Marshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Presence{Presence: &chatimv1.PresenceFrame{Entries: entries}}})
	if err != nil {
		c.server.logger.Error("encode presence frame", "error", err)
		return
	}
	if c.currentWatch() != w || !w.current(ver) {
		return
	}
	if !c.offer(outFrame{opcode: gws.OpcodeBinary, payload: payload}) {
		c.server.presenceStats.dropped[presenceDropSendFull].Add(1)
		return
	}
	c.server.presenceStats.answers.Add(1)
}
