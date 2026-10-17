package app

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/jsconsume"
)

type timerSet struct {
	members      *timer.Timers
	messages     *timer.Timers
	memberRepair *timer.MemberCountRepair
	countRepair  *timer.CountRepair
	consumer     *timer.Consumer
}

func newTimers(cfg config.Config, js timer.JetStream, log *slog.Logger) (members, messages *timer.Timers, err error) {
	t := cfg.Timer
	members, err = timer.NewTimers(js, t.Name, t.SubjectRoot, cfg.MemberCountCheckDelay, timer.WithLogger(log))
	if err != nil {
		return nil, nil, fmt.Errorf("wire member count timers: %w", err)
	}
	messages, err = timer.NewTimers(js, t.Name, t.SubjectRoot, cfg.MessageCountCheckDelay, timer.WithLogger(log))
	if err != nil {
		return nil, nil, fmt.Errorf("wire message count timers: %w", err)
	}
	return members, messages, nil
}

func wireTimers(cfg config.Config, cl *clients, st *mongostore.Store, events timer.Events, log *slog.Logger) (timerSet, error) {
	members, messages, err := newTimers(cfg, cl.js, log)
	if err != nil {
		return timerSet{}, err
	}
	ts := timerSet{members: members, messages: messages}
	ts.memberRepair, err = timer.NewMemberCountRepair(timer.MemberCountRepairDeps{Rooms: st, Counts: st, Timers: members, Events: events})
	if err != nil {
		return timerSet{}, fmt.Errorf("wire member count repair: %w", err)
	}
	ts.countRepair, err = timer.NewCountRepair(timer.CountRepairDeps{
		Messages: st, Interactions: st.Interactions(), Counts: st, Timers: messages, Rooms: st, Events: events,
	})
	if err != nil {
		return timerSet{}, fmt.Errorf("wire message count repair: %w", err)
	}
	ts.consumer, err = timer.NewConsumer(timer.ConsumerDeps{
		Source: jsconsume.NewPullSource(cl.js, cfg.Timer.Name, timer.ConsumerName), Members: ts.memberRepair, Messages: ts.countRepair,
	}, timer.ConsumerConfig{}, log)
	if err != nil {
		return timerSet{}, fmt.Errorf("wire timer consumer: %w", err)
	}
	return ts, nil
}

func (ts timerSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{
		pbconv.ReactionsCounter: func() uint64 { return ts.countRepair.Repaired(pbconv.ReactionsCounter) },
		pbconv.RepliesCounter:   func() uint64 { return ts.countRepair.Repaired(pbconv.RepliesCounter) },
		timer.MembersCounter:    ts.memberRepair.Repaired,
	}
}
