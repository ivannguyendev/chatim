package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, log *slog.Logger) (*grpcsrv.Service, error) {
	checker, err := access.NewChecker(st, nil)
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub})
	if err != nil {
		return nil, fmt.Errorf("wire mutator: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st, Events: pub, Mutator: mut, Edits: st, Hidden: st}, log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}
