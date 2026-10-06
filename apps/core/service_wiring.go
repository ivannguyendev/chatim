package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, lockedKinds []domain.Kind, log *slog.Logger) (*grpcsrv.Service, error) {
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: lockedKinds})
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	reactions := st.Reactions()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub,
		Reactions: reactions, Counter: counts,
	})
	if err != nil {
		return nil, fmt.Errorf("wire mutator: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st, Events: pub, Mutator: mut, Edits: st, Hidden: st}, log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}
