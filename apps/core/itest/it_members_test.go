package itest

import (
	"bufio"
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func addMembersAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID, requestID string, users ...string) []*chatimv1.AddedMember {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), user), func(ctx context.Context) (*chatimv1.AddMembersResponse, error) {
		return client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: roomID, Users: users, RequestId: requestID})
	})
	if err != nil {
		t.Fatalf("AddMembers(%v, %s) as %s: %v", users, requestID, user, err)
	}
	return resp.GetAdded()
}

func changeRole(t *testing.T, client chatimv1.CoreServiceClient, roomID, user string, role chatimv1.MemberRole) {
	t.Helper()
	_, err := retryingUnavailable(caller(t.Context()), func(ctx context.Context) (*chatimv1.ChangeMemberRoleResponse, error) {
		return client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: roomID, User: user, Role: role})
	})
	if err != nil {
		t.Fatalf("ChangeMemberRole(%s, %v): %v", user, role, err)
	}
}

func removeAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID, target string) *chatimv1.RemoveMemberResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), user), func(ctx context.Context) (*chatimv1.RemoveMemberResponse, error) {
		return client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: roomID, User: target})
	})
	if err != nil || !resp.GetChanged() {
		t.Fatalf("RemoveMember(%s) as %s = %v, %v; want a change", target, user, resp, err)
	}
	return resp
}

func storedMembers(t *testing.T, st *mongostore.Store, room uint64, users ...string) map[string]domain.Member {
	t.Helper()
	docs, err := st.MembersOf(t.Context(), room, users)
	if err != nil {
		t.Fatalf("MembersOf(%v): %v", users, err)
	}
	out := make(map[string]domain.Member, len(docs))
	for _, m := range docs {
		out[m.User] = m
	}
	return out
}

func assertCountMatchesDocs(t *testing.T, st *mongostore.Store, room uint64, want int) domain.Room {
	t.Helper()
	r, err := st.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get(%d): %v", room, err)
	}
	n, err := st.CountMembers(t.Context(), room)
	if err != nil || n != want || r.MemberCount != want {
		t.Fatalf("member_count = %d, active docs = %d (%v); want both %d", r.MemberCount, n, err, want)
	}
	return r
}

func liveSubject(cfg config.Config, class, roomID, kind string) string {
	return cfg.Stream.LiveRoot + "." + itTenant + "." + class + "." + roomID + ".evt." + kind
}

func itMetric(t *testing.T, cfg config.Config, series string) float64 {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+cfg.AdminAddr+"/metrics", nil)
	if err != nil {
		t.Fatalf("metrics request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	for lines.Scan() {
		if v, ok := strings.CutPrefix(lines.Text(), series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("metric %s = %q: %v", series, v, err)
			}
			return f
		}
	}
	t.Fatalf("metric %s not served (scan error %v)", series, lines.Err())
	return 0
}

func awaitMetric(t *testing.T, cfg config.Config, series string, done func(float64) bool) float64 {
	t.Helper()
	return awaitStored(t, series, func() (float64, error) { return itMetric(t, cfg, series), nil }, done)
}

func awaitSlotsSplit(t *testing.T, it *itInfra, coreIDs ...string) {
	t.Helper()
	keys := make([]string, slotmap.Count)
	for s := range slotmap.Count {
		keys[s] = slotmap.SlotKey(uint16(s))
	}
	share := slotmap.Count / len(coreIDs)
	awaitStored(t, "slot owners", func() (map[string]int, error) {
		vals, err := it.rdb.MGet(t.Context(), keys...).Result()
		owners := map[string]int{}
		for _, v := range vals {
			if s, ok := v.(string); ok {
				owners[s]++
			}
		}
		return owners, err
	}, func(owners map[string]int) bool {
		for _, id := range coreIDs {
			if owners[id] != share {
				return false
			}
		}
		return true
	})
}

func pendingTimers(t *testing.T, it *itInfra, cfg config.Config, room uint64) uint64 {
	t.Helper()
	s, err := it.js.Stream(t.Context(), cfg.Work.Name)
	if err != nil {
		t.Fatalf("work stream: %v", err)
	}
	info, err := s.Info(t.Context(), jetstream.WithSubjectFilter(cfg.Work.SubjectRoot+".timer."+pbconv.RoomID(room)+".>"))
	if err != nil {
		t.Fatalf("work stream info: %v", err)
	}
	var n uint64
	for _, c := range info.State.Subjects {
		n += c
	}
	return n
}

func assertRoomRecords(t *testing.T, records <-chan *nats.Msg, roomID string, want map[string]bool, clearedPrefix string) {
	t.Helper()
	seen, cleared := map[string]bool{}, ""
	deadline := time.After(itLiveLimit)
	var quiet <-chan time.Time
	for {
		select {
		case m := <-records:
			id := m.Header.Get(jetstream.MsgIDHeader)
			switch {
			case strings.Contains(m.Subject, ".timer.") || !strings.HasPrefix(id[min(2, len(id)):], roomID):
				continue
			case strings.HasPrefix(id, clearedPrefix) && (cleared == "" || cleared == id):
				cleared = id
			case !want[id]:
				t.Fatalf("work record %s on %s, want only %v and one %s*", id, m.Subject, slices.Sorted(maps.Keys(want)), clearedPrefix)
			default:
				seen[id] = true
			}
			if quiet == nil && cleared != "" && len(seen) == len(want) {
				quiet = time.After(itQuietWindow)
			}
		case <-quiet:
			return
		case <-deadline:
			if quiet == nil {
				t.Fatalf("work records %v and %q arrived, want %v and one %s*", slices.Sorted(maps.Keys(seen)), cleared, slices.Sorted(maps.Keys(want)), clearedPrefix)
			}
		}
	}
}
