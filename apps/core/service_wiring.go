package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const memberCountCheckDelay = 5 * time.Second

type serviceDeps struct {
	store    *mongostore.Store
	router   *actor.Router
	pub      *publish.Publisher
	cidBatch *dedupe.Batcher
	timerJS  work.TimerJetStream
	cfg      config.Config
	log      *slog.Logger
}

func wireService(d serviceDeps) (*grpcsrv.Service, error) {
	mut, err := wireMutator(d)
	if err != nil {
		return nil, err
	}
	st := d.store
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: d.router, Rooms: st, Pages: st, Events: d.pub, Mutator: mut, Edits: st, Hidden: st.Hidden()}, d.log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}

func wireMutator(d serviceDeps) (*mutate.Mutator, error) {
	st := d.store
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: d.cfg.LockedMessageKinds})
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return nil, fmt.Errorf("wire pin projector: %w", err)
	}
	requests, err := dedupe.NewRequests(d.cidBatch, d.cfg.Dedupe.CommittedTTL)
	if err != nil {
		return nil, fmt.Errorf("wire request dedupe: %w", err)
	}
	w := d.cfg.Work
	timers, err := work.NewTimers(d.timerJS, w.Name, w.SubjectRoot, w.Partitions, memberCountCheckDelay, work.WithTimerLogger(d.log))
	if err != nil {
		return nil, fmt.Errorf("wire member count timers: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st.Hidden(), Rooms: st, Events: d.pub,
		Reactions: reactions, Counter: counts, Pins: pins, Projector: projector, Limits: d.cfg.Limits,
		Members: st, Requests: requests, Forget: d.router, Timers: timers, Reads: st, Log: d.log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire mutator: %w", err)
	}
	return mut, nil
}
