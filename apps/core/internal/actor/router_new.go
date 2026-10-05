package actor

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func NewRouter(msgs store.Messages, rooms store.Rooms, sub Submitter, cids CIDRegistry, events EventPublisher, cfg Config, log *slog.Logger, opts ...Option) (*Router, error) {
	if msgs == nil || rooms == nil || sub == nil || cids == nil || events == nil {
		return nil, fmt.Errorf("%w: router needs message and room stores, a submitter, a cid registry and an event publisher", apperr.ErrInvalidArgument)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	r := &Router{
		msgs:    msgs,
		rooms:   rooms,
		sub:     sub,
		cids:    cids,
		events:  events,
		policy:  access.AllowMembers{},
		cfg:     cfg,
		log:     log,
		actors:  make(map[uint64]*actor),
		running: make(chan struct{}),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}
