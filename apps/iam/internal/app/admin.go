package app

import (
	"maps"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/config"
	"github.com/ivannguyendev/chatim/apps/iam/internal/manage"
	"github.com/ivannguyendev/chatim/pkg/metrics"
	"github.com/ivannguyendev/chatim/pkg/userlimit"
)

type adminParts struct {
	service *manage.Service
	gate    *userlimit.Gate
	users   *userlimit.Users
	stats   *api.AdminStats
}

func newAdmin(cfg config.Config, st Store) (*adminParts, error) {
	gate, err := userlimit.NewGate(cfg.AdminInflightMax)
	if err != nil {
		return nil, err
	}
	users, err := userlimit.NewUsers(userlimit.UserConfig{Rate: cfg.AdminUserRate, Burst: cfg.AdminUserBurst, Inflight: cfg.UserInflight, Capacity: cfg.UserLimiters})
	if err != nil {
		return nil, err
	}
	return &adminParts{
		service: manage.New(manage.Deps{Store: st, Now: time.Now}),
		gate:    gate, users: users, stats: &api.AdminStats{},
	}, nil
}

func adminMetricSources(a *adminParts, st Store) []metrics.Source {
	s := a.stats
	out := []metrics.Source{
		counter("admin_user_limited_total", "AdminService calls refused by the per-user rate or in-flight limit.", s.UserLimited.Load),
		counter("admin_inflight_rejected_total", "AdminService calls refused because the node in-flight cap was full.", s.InflightRejected.Load),
		counter("admin_slot_timeouts_total", "Admin transactions whose wait for an IAM_ADMIN_DB_SLOTS slot ended with the request.", st.AdminSlotTimeouts),
	}
	for label, c := range map[string]func() uint64{
		"ok": s.OK.Load, "denied": s.Denied.Load, "unauthenticated": s.Unauthenticated.Load, "bad_request": s.BadRequest.Load,
		"not_found": s.NotFound.Load, "conflict": s.Conflict.Load, "limited": s.Limited.Load, "unavailable": s.Unavailable.Load, "error": s.Errors.Load,
	} {
		out = append(out, labelledCounter("admin_calls_total", "AdminService calls by result.", "result", label, c))
	}
	denied := s.DeniedByRPC()
	for _, rpc := range slices.Sorted(maps.Keys(denied)) {
		out = append(out, labelledCounter("admin_denied_total", "AdminService calls refused for a missing permission, by RPC.", "rpc", rpc, func() uint64 { return s.DeniedByRPC()[rpc] }))
	}
	return out
}
