package interest

import (
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/frame"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

type publishDrop int

const (
	publishMalformed publishDrop = iota
	publishTenantMismatch
	publishDropCount
)

var publishDropLabels = [publishDropCount]string{"malformed", "tenant_mismatch"}

type Publisher struct {
	table     *Table
	packets   atomic.Uint64
	delivered atomic.Uint64
	dropped   [publishDropCount]atomic.Uint64
}

func NewPublisher(t *Table) *Publisher { return &Publisher{table: t} }

func (p *Publisher) Deliver(env feed.Envelope) {
	p.packets.Add(1)
	var packet chatimv1.WsPublish
	if err := proto.Unmarshal(env.Data, &packet); err != nil || packet.GetNotification() == nil || wspublish.ValidKind(packet.GetKind()) != nil {
		p.dropped[publishMalformed].Add(1)
		return
	}
	if packet.GetTenant() != env.Tenant {
		p.dropped[publishTenantMismatch].Add(1)
		return
	}
	body, err := proto.Marshal(packet.GetNotification())
	if err != nil {
		p.dropped[publishMalformed].Add(1)
		return
	}
	out := frame.Notification(body)
	users := packet.GetTargets().GetUsers()
	seen := make(map[string]struct{}, len(users))
	for _, id := range users {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		p.delivered.Add(p.toReadyUser(userKey{tenant: env.Tenant, user: id}, out, env.CatchingUp))
	}
}

func (p *Publisher) toReadyUser(k userKey, out []byte, catchingUp bool) uint64 {
	u := p.table.lookup(k)
	if u == nil {
		return 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.live() {
		return 0
	}
	return u.deliver(out, catchingUp, false)
}

func (p *Publisher) Sources() []metrics.Source {
	out := []metrics.Source{
		{Name: "publish_packets_total", Help: "WsPublish packets received from apps.", Read: counterOf(&p.packets)},
		{Name: "publish_delivered_total", Help: "Notification frames queued to connections from WsPublish packets.", Read: counterOf(&p.delivered)},
	}
	for r := range publishDropCount {
		out = append(out, metrics.Source{
			Name: "publish_dropped_total", Help: "WsPublish packets dropped before delivery, by reason.",
			Labels: map[string]string{"reason": publishDropLabels[r]}, Read: counterOf(&p.dropped[r]),
		})
	}
	return out
}
