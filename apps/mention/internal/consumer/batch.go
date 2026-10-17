package consumer

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/plan"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type job struct {
	d          jsconsume.Delivery
	ev         *chatimv1.Event
	r          plan.Result
	current    uint32
	versionErr error
}

func (c *Consumer) handle(ctx context.Context, ds []jsconsume.Delivery) {
	if len(ds) == 0 {
		return
	}
	if !c.live(ctx) {
		c.release(ds)
		return
	}
	jobs := c.prepare(ctx, ds)
	c.loadVersions(ctx, jobs)
	c.work(ctx, jobs)
}

func (c *Consumer) prepare(ctx context.Context, ds []jsconsume.Delivery) []job {
	jobs := make([]job, 0, len(ds))
	var oldest time.Time
	for _, d := range ds {
		if d.Delivered() >= StuckDeliveries {
			c.stuck.Add(1)
		}
		ev, r, err := decode(d.Data())
		if err != nil {
			c.garbage.Warn(ctx, garbageMsg, "bytes", len(d.Data()), "err", err)
			c.settle(ctx, d, err)
			continue
		}
		if oldest.IsZero() || r.Ts.Before(oldest) {
			oldest = r.Ts
		}
		if len(r.Current) == 0 && len(r.Previous) == 0 {
			c.settle(ctx, d, nil)
			continue
		}
		jobs = append(jobs, job{d: d, ev: ev, r: r})
	}
	if !oldest.IsZero() {
		c.lag.Store(int64(max(time.Since(oldest), 0)))
	}
	return jobs
}

func (c *Consumer) loadVersions(ctx context.Context, jobs []job) {
	seen := make(map[domain.MessageKey]struct{}, len(jobs))
	var keys []domain.MessageKey
	for _, j := range jobs {
		if _, dup := seen[j.r.Plan.Key]; !dup && time.Since(j.r.Ts) <= c.cfg.NotifyMaxAge {
			seen[j.r.Plan.Key] = struct{}{}
			keys = append(keys, j.r.Plan.Key)
		}
	}
	if len(keys) == 0 {
		return
	}
	versions, err := c.deps.Versions.MessageVersions(ctx, keys)
	for i := range jobs {
		if _, asked := seen[jobs[i].r.Plan.Key]; asked {
			jobs[i].current, jobs[i].versionErr = versions[jobs[i].r.Plan.Key], err
		}
	}
}

func decode(data []byte) (*chatimv1.Event, plan.Result, error) {
	ev := &chatimv1.Event{}
	if err := proto.Unmarshal(data, ev); err != nil {
		return nil, plan.Result{}, errInvalid(err)
	}
	r, err := plan.FromEvent(ev)
	if err != nil {
		return nil, plan.Result{}, errInvalid(err)
	}
	return ev, r, nil
}
