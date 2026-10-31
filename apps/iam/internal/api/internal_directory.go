package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
)

const (
	directoryChangesPath = "/internal/v1/directory/changes"
	feedLimitDefault     = 500
	feedLimitMax         = 1000
	feedRecheck          = 2 * time.Second
	feedWriteSlack       = 5 * time.Second
)

var (
	errFeedQuery = errors.New("malformed query")
	errFeedAfter = errors.New("malformed after cursor")
	errFeedLimit = errors.New("limit must be an integer from 1 to 1000")
	errFeedWait  = errors.New("wait must be a whole number of seconds, zero or more")
)

type DirectoryFeed interface {
	Changes(ctx context.Context, after store.Position, limit int) (store.ChangePage, error)
}

type DirectorySignal interface {
	Signal() <-chan struct{}
}

type FeedStats struct {
	OK           atomic.Uint64
	Empty        atomic.Uint64
	BadRequest   atomic.Uint64
	Unauthorized atomic.Uint64
	Errors       atomic.Uint64
	Waiting      atomic.Int64
}

type feedQuery struct {
	after store.Position
	limit int
	wait  time.Duration
}

type feedWake int8

const (
	feedLookAgain feedWake = iota
	feedGiveUp
	feedClientGone
)

type feedPage struct {
	Changes []any  `json:"changes"`
	Next    string `json:"next"`
}

type feedUser struct {
	Kind        string   `json:"kind"`
	TenantID    string   `json:"tenant_id"`
	ID          string   `json:"id"`
	Ver         int64    `json:"ver"`
	State       string   `json:"state"`
	DisplayName string   `json:"display_name"`
	AvatarURL   string   `json:"avatar_url"`
	Groups      []string `json:"groups"`
}

type feedGroup struct {
	Kind        string `json:"kind"`
	TenantID    string `json:"tenant_id"`
	ID          string `json:"id"`
	Ver         int64  `json:"ver"`
	State       string `json:"state"`
	Name        string `json:"name"`
	ParentID    string `json:"parent_id"`
	Mentionable bool   `json:"mentionable"`
}

func (in *Internal) directoryChanges(w http.ResponseWriter, r *http.Request) {
	stats := in.opts.FeedStats
	if !in.authorized(r.Header.Get("Authorization")) {
		stats.Unauthorized.Add(1)
		writeUnauthorized(w)
		return
	}
	q, err := in.parseFeedQuery(r.URL.RawQuery)
	if err != nil {
		stats.BadRequest.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	in.holdConnection(r.Context(), w, q.wait)
	page, err := in.awaitChanges(r.Context(), q)
	switch {
	case r.Context().Err() != nil:
		return
	case err != nil:
		stats.Errors.Add(1)
		in.opts.Log.WarnContext(r.Context(), "directory feed unavailable", "error", err)
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "directory feed unavailable, retry later"})
		return
	}
	writeJSON(w, http.StatusOK, in.feedAnswer(q.after, page))
}

func (in *Internal) parseFeedQuery(raw string) (feedQuery, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return feedQuery{}, errFeedQuery
	}
	for _, v := range values {
		if len(v) > 1 {
			return feedQuery{}, errFeedQuery
		}
	}
	q := feedQuery{limit: feedLimitDefault}
	if q.after, err = directory.DecodePosition(values.Get("after")); err != nil {
		return feedQuery{}, errFeedAfter
	}
	if values.Has("limit") {
		n, err := strconv.ParseUint(values.Get("limit"), 10, 16)
		if err != nil || n < 1 || n > feedLimitMax {
			return feedQuery{}, errFeedLimit
		}
		q.limit = int(n)
	}
	if values.Has("wait") {
		if q.wait, err = in.cappedWait(values.Get("wait")); err != nil {
			return feedQuery{}, err
		}
	}
	return q, nil
}

func (in *Internal) cappedWait(raw string) (time.Duration, error) {
	if raw == "" || raw[0] < '0' || raw[0] > '9' {
		return 0, errFeedWait
	}
	seconds, err := strconv.ParseInt(raw, 10, 32)
	switch {
	case errors.Is(err, strconv.ErrRange):
		return in.opts.FeedWaitMax, nil
	case err != nil:
		return 0, errFeedWait
	}
	return min(time.Duration(seconds)*time.Second, in.opts.FeedWaitMax), nil
}

func (in *Internal) holdConnection(ctx context.Context, w http.ResponseWriter, wait time.Duration) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		in.opts.Log.DebugContext(ctx, "directory feed read deadline not cleared", "error", err)
	}
	if err := rc.SetWriteDeadline(time.Now().Add(wait + feedWriteSlack)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		in.opts.Log.DebugContext(ctx, "directory feed write deadline not set", "error", err)
	}
}

func (in *Internal) awaitChanges(ctx context.Context, q feedQuery) (store.ChangePage, error) {
	deadlineAt := time.Now().Add(q.wait)
	deadline := time.NewTimer(q.wait)
	defer deadline.Stop()
	for {
		signal := in.opts.DirectorySignal.Signal()
		page, err := in.opts.DirectoryFeed.Changes(ctx, q.after, q.limit)
		if err != nil || len(page.Changes) > 0 || q.wait == 0 || !time.Now().Before(deadlineAt) || in.stopping() {
			return page, err
		}
		switch in.sleep(ctx, signal, deadline.C) {
		case feedLookAgain:
		case feedGiveUp:
			return store.ChangePage{}, nil
		case feedClientGone:
			return store.ChangePage{}, ctx.Err()
		}
	}
}

func (in *Internal) sleep(ctx context.Context, signal <-chan struct{}, deadline <-chan time.Time) feedWake {
	recheck := time.NewTimer(feedRecheck)
	defer recheck.Stop()
	in.opts.FeedStats.Waiting.Add(1)
	defer in.opts.FeedStats.Waiting.Add(-1)
	select {
	case <-signal:
		return feedLookAgain
	case <-recheck.C:
		return feedLookAgain
	case <-deadline:
		return feedGiveUp
	case <-in.stop:
		return feedGiveUp
	case <-ctx.Done():
		return feedClientGone
	}
}

func (in *Internal) stopping() bool {
	select {
	case <-in.stop:
		return true
	default:
		return false
	}
}

func (in *Internal) feedAnswer(after store.Position, page store.ChangePage) feedPage {
	if len(page.Changes) == 0 {
		in.opts.FeedStats.Empty.Add(1)
		return feedPage{Changes: []any{}, Next: directory.EncodePosition(after)}
	}
	in.opts.FeedStats.OK.Add(1)
	records := make([]any, 0, len(page.Changes))
	for _, c := range page.Changes {
		if record, ok := feedRecord(c); ok {
			records = append(records, record)
		}
	}
	return feedPage{Changes: records, Next: directory.EncodePosition(page.Next)}
}

func feedRecord(c store.Change) (any, bool) {
	switch {
	case c.Kind == store.ChangeUser && c.User != nil:
		groups := c.User.Groups
		if groups == nil {
			groups = []string{}
		}
		return feedUser{Kind: "user", TenantID: c.TenantID, ID: c.ID, Ver: c.Ver, State: userStateName(c.User.State),
			DisplayName: c.User.DisplayName, AvatarURL: c.User.AvatarURL, Groups: groups}, true
	case c.Kind == store.ChangeGroup && c.Group != nil:
		return feedGroup{Kind: "group", TenantID: c.TenantID, ID: c.ID, Ver: c.Ver, State: groupStateName(c.Group.State),
			Name: c.Group.Name, ParentID: c.Group.ParentID, Mentionable: c.Group.Mentionable}, true
	default:
		return nil, false
	}
}

func userStateName(s store.State) string {
	if s == store.StateActive {
		return "active"
	}
	return "disabled"
}

func groupStateName(s store.GroupState) string {
	if s == store.GroupLive {
		return "live"
	}
	return "deleted"
}
