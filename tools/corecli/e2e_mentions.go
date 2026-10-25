package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const (
	saleGroup  = "team-sale"
	saleName   = "Team Sale"
	hrGroup    = "hr-internal"
	hrName     = "HR Internal"
	catalogVer = 1
)

var errMentionsTwice = errors.New("this scenario already ran its mention phase")

type mentionRun struct {
	*linkRun
	notes      *noticeFeed
	subject    string
	catalogTTL time.Duration
	sender     string
	an         string
	binh       string
	chi        string
	minh       string
	room       string
	sale       uint64
	edited     uint64
	notices    []e2e.Notice
}

func e2eMentions(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e mentions", flag.ContinueOnError)
	o := addOptions(fs)
	live := addLiveFlags(fs)
	root := fs.String("ws-publish-root", wspublish.DefaultRoot, "ws publish subject root, MENTION_WS_PUBLISH_ROOT of the cores")
	catalogTTL := fs.Duration("catalog-ttl", 60*time.Second, "how long a core keeps the group catalog before it reads it again")
	dir := fs.String("state", "/state", "directory holding the scenario state")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for the notifications and lists of each step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if st.MentionRun {
		return errMentionsTwice
	}
	subject, err := wspublish.Subject(*root, st.Tenant)
	if err != nil {
		return err
	}
	feed, err := openNotices(*live.url, subject)
	if err != nil {
		return err
	}
	defer feed.Close()
	r := &mentionRun{
		linkRun: &linkRun{memberRun: &memberRun{st: &st, wait: *wait}}, notes: feed, subject: subject, catalogTTL: *catalogTTL,
		sender: "e2e-lan-" + st.Room, an: "e2e-an-" + st.Room, binh: "e2e-binh-" + st.Room, chi: "e2e-chi-" + st.Room, minh: "e2e-minh-" + st.Room,
	}
	err = withRoutes(ctx, o, func(ctx context.Context, s *session) error {
		r.base, r.cl = ctx, s.client
		return r.run()
	})
	if err != nil {
		return err
	}
	st.MentionRun = true
	fmt.Fprintf(os.Stderr, "mention phase ok on group %s: %d notifications checked by id, user, kind, subject and target\n", r.room, len(r.notices))
	return e2e.Save(statePath(*dir), st)
}

func (r *mentionRun) run() error {
	steps := []func() error{
		r.setGroups, r.setUserGroups, r.mentionSale, r.mentionInactive, r.editAddsMinh,
		r.editDropsMinh, r.deleteRetracts, r.mentionAll, r.roomGroups, r.olderUserGroups,
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("mention step %d: %w", i+1, err)
		}
		fmt.Fprintf(os.Stderr, "mention step %d ok\n", i+1)
	}
	return nil
}

func (r *mentionRun) expectNotices(ns ...e2e.Notice) error {
	for _, n := range ns {
		n.Subject = r.subject
		r.notices = append(r.notices, n)
	}
	return r.notes.await(r.base, r.room, r.notices, r.wait)
}

func (r *mentionRun) sendMentions(cid string, set *chatimv1.MentionSet) (uint64, error) {
	return r.send(r.room, r.sender, cid, func(req *chatimv1.SendMessageRequest) { req.Mentions = set })
}

func (r *mentionRun) mentionsOf(user string, groups ...string) ([]*chatimv1.MentionItem, error) {
	req := &chatimv1.ListMentionsRequest{Limit: linkPage}
	for _, g := range groups {
		req.Groups = append(req.Groups, &chatimv1.MentionGroup{Id: g})
	}
	resp, _, err := r.cl.ListMentions(r.as(user), req)
	if err != nil {
		return nil, fmt.Errorf("mentions of %s: %w", user, err)
	}
	return resp.GetItems(), nil
}

func (r *mentionRun) setUserGroupsAs(items ...*chatimv1.UserGroupsItem) ([]string, error) {
	resp, _, err := r.cl.SetUserGroups(r.as(r.sender), &chatimv1.SetUserGroupsRequest{Items: items})
	if err != nil {
		return nil, fmt.Errorf("set user groups: %w", err)
	}
	return resp.GetChangedUsers(), nil
}

func (r *mentionRun) awaitCatalog() error {
	fmt.Fprintf(os.Stderr, "group catalog changed: waiting %v so every core reads it again\n", r.catalogTTL)
	if !backoff.Pause(r.base, r.catalogTTL+time.Second) {
		return r.base.Err()
	}
	return nil
}

func userItem(user string, ver uint64, groups ...string) *chatimv1.UserGroupsItem {
	return &chatimv1.UserGroupsItem{User: user, Groups: groups, Ver: ver}
}

func targets(kind chatimv1.MentionKind, ids ...string) *chatimv1.MentionSet {
	set := &chatimv1.MentionSet{}
	for _, id := range ids {
		set.Targets = append(set.Targets, &chatimv1.MentionTarget{Kind: kind, Id: id})
	}
	return set
}
