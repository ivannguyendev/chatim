package recipients_test

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/recipients"
)

const room uint64 = 777

var at = time.UnixMilli(1_700_000_000_000).UTC()

func user(id string) domain.Target  { return domain.Target{Kind: domain.TargetUser, ID: id, At: at} }
func group(id string) domain.Target { return domain.Target{Kind: domain.TargetGroup, ID: id, At: at} }

var everyone = domain.Target{Kind: domain.TargetAll, At: at}

func resolve(t *testing.T, f *fixture, rt domain.RoomType, targets ...domain.Target) map[string]domain.Target {
	t.Helper()
	got, err := f.r.Resolve(t.Context(), tenant, room, rt, "Lan", targets)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return got
}

func expect(t *testing.T, got, want map[string]domain.Target) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("recipients = %v,\nwant %v", got, want)
	}
}

func TestUsersStillInTheRoomExceptTheSender(t *testing.T) {
	f := newFixture(t)
	f.core.join(room, "Lan", "Minh", "Tu")
	f.core.leave(room, "Tu")
	got := resolve(t, f, domain.RoomGroup, user("Minh"), user("Tu"), user("Ghost"), user("Lan"))
	expect(t, got, map[string]domain.Target{"Minh": user("Minh")})
	if f.core.membersOf != 1 || f.core.listCalls != 0 {
		t.Fatalf("calls MembersOf %d, ListMembers %d; want 1, 0", f.core.membersOf, f.core.listCalls)
	}
}

func TestOnlyTaggableCatalogGroupsNotify(t *testing.T) {
	f := newFixture(t)
	f.core.join(room, "Lan", "Ha", "Minh", "Tu")
	f.catalogue(t, "team-sale", true)
	f.catalogue(t, "hr", false)
	f.inGroups(t, "Ha", "team-sale")
	f.inGroups(t, "Minh", "hr")
	f.inGroups(t, "Tu", "ghost")
	f.inGroups(t, "Lan", "team-sale")
	got := resolve(t, f, domain.RoomGroup, group("team-sale"), group("hr"), group("ghost"))
	expect(t, got, map[string]domain.Target{"Ha": group("team-sale")})
	f2 := newFixture(t)
	f2.core.join(room, "Minh")
	f2.inGroups(t, "Minh", "hr")
	f2.catalogue(t, "hr", false)
	expect(t, resolve(t, f2, domain.RoomGroup, group("hr")), map[string]domain.Target{})
	if f2.core.listCalls != 0 {
		t.Fatalf("an untaggable group read the room: %d ListMembers calls", f2.core.listCalls)
	}
}

func TestEveryoneInDMAndGroupButNotInChannel(t *testing.T) {
	f := newFixture(t)
	f.core.join(room, "Lan", "Minh", "Tu", "Ha")
	f.core.leave(room, "Ha")
	want := map[string]domain.Target{"Minh": everyone, "Tu": everyone}
	expect(t, resolve(t, f, domain.RoomGroup, everyone), want)
	expect(t, resolve(t, f, domain.RoomDM, everyone), want)
	calls := f.core.listCalls
	expect(t, resolve(t, f, domain.RoomChannel, everyone, user("Minh")), map[string]domain.Target{"Minh": user("Minh")})
	if f.core.listCalls != calls {
		t.Fatalf("channel @all listed the room")
	}
}

func TestOneEntryPerUserByPriority(t *testing.T) {
	f := newFixture(t)
	f.core.join(room, "Lan", "Minh", "Ha", "Tu")
	f.catalogue(t, "team-sale", true)
	f.catalogue(t, "a-team", true)
	f.inGroups(t, "Minh", "team-sale")
	f.inGroups(t, "Ha", "team-sale", "a-team")
	got := resolve(t, f, domain.RoomGroup, everyone, group("team-sale"), user("Minh"), group("a-team"))
	expect(t, got, map[string]domain.Target{"Minh": user("Minh"), "Ha": group("a-team"), "Tu": everyone})
}

func TestSmallAndLargeRoomsGiveTheSameRecipients(t *testing.T) {
	staff := users("s", 0, 2100)
	for _, size := range []int{recipients.SmallRoom, recipients.PageSize, recipients.SmallRoom + 500} {
		f := newFixture(t)
		f.catalogue(t, "staff", true)
		f.catalogue(t, "nobody", true)
		items := make([]domain.UserGroups, 0, len(staff))
		for _, u := range staff {
			items = append(items, domain.UserGroups{Tenant: tenant, User: u, Groups: []string{"staff"}, Ver: 1})
		}
		if _, err := f.groups.Set(t.Context(), items); err != nil {
			t.Fatalf("set staff: %v", err)
		}
		members := append(users("s", 1900, 1900+size/2), users("x", 0, size-size/2)...)
		f.core.join(room, members...)
		want := map[string]domain.Target{}
		for _, u := range users("s", 1900, 2100) {
			want[u] = group("staff")
		}
		f.groups.of, f.groups.byGroup = 0, 0
		got := resolve(t, f, domain.RoomGroup, group("nobody"), group("staff"))
		expect(t, got, want)
		small := size <= recipients.SmallRoom
		if small != (f.groups.of == 1 && f.groups.byGroup == 0) || !small && f.groups.byGroup != 4 {
			t.Fatalf("room of %d: Of %d, MembersOfGroup %d calls", size, f.groups.of, f.groups.byGroup)
		}
		if f.core.listLimits[0] != recipients.PageSize || recipients.PageSize > coreMemberPage {
			t.Fatalf("first ListMembers limit = %d, want one page of %d within the core page %d", f.core.listLimits[0], recipients.PageSize, coreMemberPage)
		}
	}
}

func TestPortErrorsPropagate(t *testing.T) {
	for _, step := range []string{"MembersOf", "ListMembers"} {
		f := newFixture(t)
		f.core.join(room, "Minh")
		f.core.failOn = step
		_, err := f.r.Resolve(t.Context(), tenant, room, domain.RoomGroup, "Lan", []domain.Target{user("Minh"), everyone})
		if !errors.Is(err, errDown) {
			t.Fatalf("%s failing: Resolve = %v, want the port error", step, err)
		}
	}
	f := newFixture(t)
	_, err := f.r.Resolve(t.Context(), "", room, domain.RoomGroup, "Lan", []domain.Target{group("staff")})
	if err == nil {
		t.Fatalf("Resolve with a bad tenant read the catalog without an error")
	}
}

func TestLeftMembersInTheFirstPageStillPickTheLargePath(t *testing.T) {
	f := newFixture(t)
	f.catalogue(t, "staff", true)
	left := users("a", 0, 50)
	f.core.join(room, left...)
	for _, u := range left {
		f.core.leave(room, u)
	}
	f.core.join(room, users("m", 0, recipients.SmallRoom-len(left)+1)...)
	late := users("z", 0, 100)
	f.core.join(room, late...)
	items := make([]domain.UserGroups, 0, len(late))
	for _, u := range append(slices.Clone(late), left...) {
		items = append(items, domain.UserGroups{Tenant: tenant, User: u, Groups: []string{"staff"}, Ver: 1})
	}
	if _, err := f.groups.Set(t.Context(), items); err != nil {
		t.Fatalf("set staff: %v", err)
	}
	want := map[string]domain.Target{}
	for _, u := range late {
		want[u] = group("staff")
	}
	expect(t, resolve(t, f, domain.RoomGroup, group("staff")), want)
	if f.groups.byGroup == 0 {
		t.Fatalf("a room of %d rows took the small path", len(f.core.rooms[room]))
	}
}
