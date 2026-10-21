package consumer

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/plan"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Consumer) process(ctx context.Context, j *job) error {
	r := j.r
	if err := c.deps.Mentions.Apply(ctx, r.Plan); err != nil {
		return err
	}
	if at, ok := sharedAt(r.Current); ok {
		if err := c.deps.Rooms.Raise(ctx, r.Plan.Tenant, r.Plan.Key.Room, at); err != nil {
			return err
		}
	}
	if !c.gate.Fresh(r.Ts) {
		return nil
	}
	if j.versionErr != nil {
		return j.versionErr
	}
	if !c.gate.Latest(r.Plan.Ver, j.current) {
		return nil
	}
	return c.publish(ctx, j)
}

func (c *Consumer) publish(ctx context.Context, j *job) error {
	r := j.r
	current, err := c.resolve(ctx, r, r.Current)
	if err != nil {
		return err
	}
	previous, err := c.resolve(ctx, r, r.Previous)
	if err != nil {
		return err
	}
	add, drop := plan.Diff(current, previous)
	if len(add) == 0 && len(drop) == 0 {
		return nil
	}
	notified, err := c.build.Notified(r, messageOf(j.ev), add)
	if err != nil {
		return errInvalid(err)
	}
	retracted, err := c.build.Retracted(r, drop)
	if err != nil {
		return errInvalid(err)
	}
	b := c.pub.Batch()
	for _, n := range append(notified, retracted...) {
		if err := b.Add(n); err != nil {
			return errors.Join(err, b.Wait(ctx))
		}
	}
	return b.Wait(ctx)
}

func (c *Consumer) resolve(ctx context.Context, r plan.Result, targets []domain.Target) (map[string]domain.Target, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	return c.deps.Resolver.Resolve(ctx, r.Plan.Tenant, r.Plan.Key.Room, r.RoomType, r.Sender, targets)
}

func sharedAt(targets []domain.Target) (time.Time, bool) {
	var at time.Time
	for _, t := range targets {
		if t.Kind != domain.TargetUser && t.At.After(at) {
			at = t.At
		}
	}
	return at, !at.IsZero()
}

func messageOf(ev *chatimv1.Event) *chatimv1.Message {
	switch p := ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return p.MessageCreated.GetMessage()
	case *chatimv1.Event_MessageEdited:
		return p.MessageEdited.GetMessage()
	case *chatimv1.Event_MessageDeleted:
		return p.MessageDeleted.GetMessage()
	default:
		return nil
	}
}
