package mentionapp

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/mention/internal/consumer"
	"github.com/ivannguyendev/chatim/apps/mention/internal/groups"
	"github.com/ivannguyendev/chatim/apps/mention/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/mention/internal/recipients"
	"github.com/ivannguyendev/chatim/apps/mention/internal/roomgroups"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store/mongostore"
)

func (a *App) assemble() error {
	db := a.deps.Mongo
	mentions, rooms := mongostore.NewMentions(db), mongostore.NewRooms(db)
	userGroups, groupStore := mongostore.NewUserGroups(db), mongostore.NewGroups(db)
	catalog := groups.New(groupStore)
	c, err := consumer.New(a.cfg, consumer.Deps{
		Source: consumer.NewSource(a.deps.JS, a.cfg), Mentions: mentions, Rooms: rooms, Versions: a.deps.Core,
		Resolver: recipients.Resolver{Core: a.deps.Core, UserGroups: userGroups, Catalog: catalog},
		Notify:   a.deps.NotifyJS, Log: a.deps.Log,
	})
	if err != nil {
		return fmt.Errorf("mention consumer: %w", err)
	}
	builder, err := roomgroups.NewBuilder(a.deps.Core, userGroups)
	if err != nil {
		return fmt.Errorf("mention room groups builder: %w", err)
	}
	sets, err := roomgroups.NewSets(roomgroups.Config{SyncMax: a.cfg.RoomGroupsSyncMax, TTL: a.cfg.RoomGroupsTTL, Rebuild: a.cfg.RoomGroupsRebuild}, builder.Build, a.deps.Log)
	if err != nil {
		return fmt.Errorf("mention room groups: %w", err)
	}
	watch, err := roomgroups.NewWatch(a.deps.NATS, a.cfg.LiveRoot, sets.Forget, a.deps.Log)
	if err != nil {
		sets.Close()
		return fmt.Errorf("mention room groups watch: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{
		Core: a.deps.Core, Mentions: mentions, Rooms: rooms, UserGroups: userGroups, Groups: groupStore,
		Catalog: catalog, RoomGroups: sets, HideRead: a.cfg.HideReadTenants,
	})
	if err != nil {
		sets.Close()
		return fmt.Errorf("mention service: %w", err)
	}
	a.consumer, a.sets, a.watch, a.service = c, sets, watch, svc
	return nil
}
