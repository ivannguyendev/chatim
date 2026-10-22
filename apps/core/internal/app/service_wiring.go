package app

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/api/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/core/internal/interaction/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/member"
	"github.com/ivannguyendev/chatim/apps/core/internal/message"
	"github.com/ivannguyendev/chatim/apps/core/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/room"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

type serviceDeps struct {
	store     *mongostore.Store
	router    *actor.Router
	pub       *publish.Publisher
	reads     member.ReadSink
	cidBatch  *dedupe.Batcher
	timers    *timer.Timers
	msgTimers *timer.Timers
	rooms     roomCaches
	cfg       config.Config
	log       *slog.Logger
}

func wireService(d serviceDeps) (*grpcsrv.Service, *message.Reader, error) {
	requests, err := dedupe.NewRequests(d.cidBatch, d.cfg.Dedupe.CommittedTTL)
	if err != nil {
		return nil, nil, fmt.Errorf("wire request dedupe: %w", err)
	}
	st := d.store
	projector, err := pinproj.New(st.Pins(), st)
	if err != nil {
		return nil, nil, fmt.Errorf("wire pin projector: %w", err)
	}
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: d.cfg.LockedMessageKinds})
	if err != nil {
		return nil, nil, fmt.Errorf("wire access checker: %w", err)
	}
	editor, err := wireMessageEditor(d, checker)
	if err != nil {
		return nil, nil, err
	}
	reader, err := message.NewReader(message.ReaderDeps{Access: checker, Pages: st, Edits: st, Hidden: st.Hidden(), Interactions: st.Interactions()})
	if err != nil {
		return nil, nil, fmt.Errorf("wire message reader: %w", err)
	}
	interactions, err := wireInteractionService(d, checker, projector)
	if err != nil {
		return nil, nil, err
	}
	members, err := wireMemberService(d, checker, requests)
	if err != nil {
		return nil, nil, err
	}
	roomService, err := wireRoomService(d, checker, members, requests)
	if err != nil {
		return nil, nil, err
	}
	readers, err := wireReaders(d, checker, reader, interactions, projector)
	if err != nil {
		return nil, nil, err
	}
	rooms, err := wireRoomList(st, d.rooms, d.cfg.RoomList.JoinMax)
	if err != nil {
		return nil, nil, err
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{
		Sender: d.router, Editor: editor, Reader: reader, InteractionService: interactions, InteractionReader: readers.interactions,
		MemberService: members, MemberReader: readers.members, RoomService: roomService, RoomList: rooms,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, reader, nil
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
		Forget: d.router, Timers: d.timers, Reads: st, ReadSink: d.reads, Limits: d.cfg.Members, Log: d.log,
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

type domainReaders struct {
	interactions *interaction.Reader
	members      *member.Reader
}

func wireReaders(d serviceDeps, checker *access.Checker, views *message.Reader, settings *interaction.Service, projector *pinproj.Projector) (domainReaders, error) {
	st := d.store
	interactions, err := interaction.NewReader(interaction.ReaderDeps{
		Access: checker, Messages: st, Views: views, Interactions: st.Interactions(), Threads: st.ReplyThreads(), Pins: projector, Settings: settings,
	})
	if err != nil {
		return domainReaders{}, fmt.Errorf("wire interaction reader: %w", err)
	}
	members, err := member.NewReader(member.ReaderDeps{Access: checker, Members: st})
	if err != nil {
		return domainReaders{}, fmt.Errorf("wire member reader: %w", err)
	}
	return domainReaders{interactions: interactions, members: members}, nil
}
