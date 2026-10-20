package app

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/change/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

type serviceDeps struct {
	store     *mongostore.Store
	router    *actor.Router
	pub       *publish.Publisher
	cidBatch  *dedupe.Batcher
	timers    *timer.Timers
	msgTimers *timer.Timers
	rooms     roomCaches
	cfg       config.Config
	log       *slog.Logger
}

func wireService(d serviceDeps) (*grpcsrv.Service, error) {
	requests, err := dedupe.NewRequests(d.cidBatch, d.cfg.Dedupe.CommittedTTL)
	if err != nil {
		return nil, fmt.Errorf("wire request dedupe: %w", err)
	}
	st := d.store
	projector, err := pinproj.New(st.Pins(), st)
	if err != nil {
		return nil, fmt.Errorf("wire pin projector: %w", err)
	}
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: d.cfg.LockedMessageKinds})
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	editor, err := wireMessageEditor(d, checker)
	if err != nil {
		return nil, err
	}
	interactions, err := wireInteractionService(d, checker, projector)
	if err != nil {
		return nil, err
	}
	members, err := wireMemberService(d, checker, requests)
	if err != nil {
		return nil, err
	}
	roomService, err := wireRoomService(d, checker, members, requests)
	if err != nil {
		return nil, err
	}
	rooms, err := wireRoomList(st, d.rooms, d.cfg.RoomList.JoinMax)
	if err != nil {
		return nil, err
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{
		Sender: d.router, Rooms: st, Pages: st, Editor: editor, InteractionService: interactions, MemberService: members, RoomService: roomService, Edits: st, Hidden: st.Hidden(), Bookmarks: st.Interactions(),
		Replies: st.Interactions(), Mine: st.Interactions(), RoomList: rooms,
		Threads: st.ReplyThreads(), Reactors: st.Interactions(), PinBoard: projector, MemberList: st,
	}, d.log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}

func wireMessageEditor(d serviceDeps, checker *access.Checker) (*message.Editor, error) {
	st := d.store
	editor, err := message.New(message.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st.Hidden(), Events: d.pub, Interactions: st.Interactions(), Limits: d.cfg.Messages,
	})
	if err != nil {
		return nil, fmt.Errorf("wire message editor: %w", err)
	}
	return editor, nil
}

func wireInteractionService(d serviceDeps, checker *access.Checker, projector *pinproj.Projector) (*interaction.Service, error) {
	st := d.store
	svc, err := interaction.New(interaction.Deps{
		Access: checker, Messages: st, Interactions: st.Interactions(), Counts: st, CountTimers: d.msgTimers,
		Pins: st.Pins(), Projector: projector, Events: d.pub, Limits: d.cfg.Interactions, Log: d.log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire interaction service: %w", err)
	}
	return svc, nil
}

func wireMemberService(d serviceDeps, checker *access.Checker, requests *dedupe.Requests) (*member.Service, error) {
	st := d.store
	svc, err := member.New(member.Deps{
		Access: checker, Messages: st, Rooms: st, Events: d.pub, Members: st, Requests: requests,
		Forget: d.router, Timers: d.timers, Reads: st, Limits: d.cfg.Members, Log: d.log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire member service: %w", err)
	}
	return svc, nil
}

func wireRoomService(d serviceDeps, checker *access.Checker, members *member.Service, requests *dedupe.Requests) (*room.Service, error) {
	st := d.store
	svc, err := room.New(room.Deps{
		Access: checker, Rooms: st, Directs: st.DirectRooms(), Members: members, Requests: requests, Events: d.pub, Log: d.log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire room service: %w", err)
	}
	return svc, nil
}
