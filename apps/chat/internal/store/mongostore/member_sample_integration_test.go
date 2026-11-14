package mongostore

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
)

func TestMemberSampleIsACoveredIndexScan(t *testing.T) {
	s, db, capture := monitoredStore(t)
	const room = uint64(7_350_000_001)
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Kho", CreatedBy: "u00", CreatedAt: at, MemberCount: 30, MemberCountVer: 1, InfoVer: 1}
	members := make([]domain.Member, 30)
	for i := range members {
		members[i] = domain.Member{Room: room, Tenant: "acme", User: fmt.Sprintf("u%02d", i), Role: domain.RoleMember, JoinedAt: at.Add(time.Duration(i) * time.Second)}
	}
	members[0].Role = domain.RoleOwner
	if err := s.Create(t.Context(), r, members); err != nil {
		t.Fatal(err)
	}
	capture.reset()
	got, err := s.MemberSample(t.Context(), room, domain.MaxAvatarUsers)
	if err != nil || len(got) != domain.MaxAvatarUsers {
		t.Fatalf("MemberSample = %v, %v", got, err)
	}
	c := explainLast(t, db, capture, "members")
	t.Logf("plan %s", c.plan)
	if c.examined != 0 || c.returned != domain.MaxAvatarUsers || !strings.Contains(c.plan, "IXSCAN") ||
		!strings.Contains(c.plan, "PROJECTION_COVERED") || strings.Contains(c.plan, "COLLSCAN") || strings.Contains(c.plan, `"SORT"`) {
		t.Fatalf("examined %d docs, returned %d, plan %s; want a covered IXSCAN returning %d", c.examined, c.returned, c.plan, domain.MaxAvatarUsers)
	}
}
