package roomlist_test

import (
	"fmt"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomcache"
	"github.com/ivannguyendev/chatim/apps/core/internal/room/roomlist"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	acme   = "acme"
	globex = "globex"
	minh   = "minh"
	lan    = "lan"
	hoa    = "hoa"

	shop    uint64 = 777
	direct  uint64 = 888
	depot   uint64 = 999
	old     uint64 = 555
	left    uint64 = 666
	foreign uint64 = 4444
	unknown uint64 = 123
)

var day = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func clock(h, m int) time.Time {
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

func newWorld() *countingStore {
	return &countingStore{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), hidden: memstore.NewHidden()}
}

func newLister(t *testing.T, st *countingStore, joinMax int) *roomlist.Lister {
	t.Helper()
	info, err := roomcache.NewInfoCache(10_000)
	if err != nil {
		t.Fatal(err)
	}
	tails, err := roomcache.NewTailCache(1 << 26)
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := roomcache.NewSnapshotCache(1 << 22)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := roomcache.NewPathCache(1_000)
	if err != nil {
		t.Fatal(err)
	}
	l, err := roomlist.New(roomlist.Deps{List: st, Tails: st, Hidden: st, Info: info, Tail: tails, Snapshots: snaps, Paths: paths, JoinMax: joinMax})
	if err != nil {
		t.Fatalf("roomlist.New: %v", err)
	}
	return l
}

func person(room uint64, tenant, user string, role domain.Role, readSeq uint64) domain.Member {
	return domain.Member{Room: room, Tenant: tenant, User: user, Role: role, JoinedAt: day.AddDate(0, -1, 0), ReadSeq: readSeq}
}

func group(id uint64, tenant, name string) domain.Room {
	return domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: name, CreatedBy: "lan", CreatedAt: day.AddDate(0, -2, 0), MemberCount: 2}
}

func (c *countingStore) create(t *testing.T, r domain.Room, members ...domain.Member) {
	t.Helper()
	if err := c.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create(%d): %v", r.ID, err)
	}
}

func (c *countingStore) post(t *testing.T, room uint64, tenant string, seq uint64, from string, at time.Time) {
	t.Helper()
	msg := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: from, CID: fmt.Sprintf("c-%d-%d", room, seq), Text: fmt.Sprintf("tin %d", seq), CreatedAt: at}
	if res := c.msgs.Insert(t.Context(), []domain.Message{msg}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert(%d/%d) = %+v", room, seq, res[0])
	}
	if err := c.rooms.TouchActivity(t.Context(), []store.Activity{{Room: room, Seq: seq, At: at}}); err != nil {
		t.Fatalf("TouchActivity(%d): %v", room, err)
	}
}

func (c *countingStore) hide(t *testing.T, room, seq uint64) {
	t.Helper()
	if _, err := c.hidden.Hide(t.Context(), minh, store.MsgKey{Room: room, Seq: seq}, day); err != nil {
		t.Fatalf("Hide(%d/%d): %v", room, seq, err)
	}
}

func (c *countingStore) leave(t *testing.T, room uint64, user string) {
	t.Helper()
	cur, err := c.rooms.Member(t.Context(), room, user)
	if err != nil {
		t.Fatalf("Member(%d, %s): %v", room, user, err)
	}
	next := cur.Next(cur.Role, domain.MemberRemoved, cur.Priority, "leave", user, day)
	if ok, err := c.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("ApplyMember(%d, %s) = %v, %v", room, user, ok, err)
	}
}

func seedMinh(t *testing.T, shopLast uint64) *countingStore {
	t.Helper()
	c := newWorld()
	me := person(shop, acme, minh, domain.RoleAdmin, 41)
	c.create(t, group(shop, acme, "Cửa hàng 12"), person(shop, acme, lan, domain.RoleOwner, 45), me)
	for seq := uint64(1); seq <= shopLast; seq++ {
		from, at := lan, clock(9, int(seq))
		if seq == 43 {
			from = minh
		}
		if seq == 45 {
			at = clock(10, 5)
		}
		c.post(t, shop, acme, seq, from, at)
	}
	c.hide(t, shop, 44)
	seedDirect(t, c)
	seedDepotAndOld(t, c)
	seedNoise(t, c)
	return c
}

func seedDirect(t *testing.T, c *countingStore) {
	t.Helper()
	dm := domain.Room{ID: direct, Tenant: acme, Type: domain.RoomDM, DMKey: domain.DirectKey(acme, minh, lan), CreatedBy: minh, CreatedAt: day.AddDate(0, -2, 0), MemberCount: 2}
	me := person(direct, acme, minh, domain.RoleMember, 12)
	me.Preferences.PinnedAt = clock(8, 0)
	c.create(t, dm, me, person(direct, acme, lan, domain.RoleMember, 10))
	for seq := uint64(1); seq <= 12; seq++ {
		from := []string{lan, minh}[seq%2]
		c.post(t, direct, acme, seq, from, clock(9, 28+int(seq)))
	}
}

func seedDepotAndOld(t *testing.T, c *countingStore) {
	t.Helper()
	me := person(depot, acme, minh, domain.RoleMember, 230)
	me.ClearedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c.create(t, group(depot, acme, "Kho"), person(depot, acme, hoa, domain.RoleOwner, 236), me)
	for seq := uint64(221); seq <= 236; seq++ {
		at := time.Date(2026, 9, 30, 12, int(seq-221), 0, 0, time.UTC)
		if seq > 230 {
			at = day.AddDate(0, 0, -1).Add(15*time.Hour + time.Duration(seq)*time.Minute)
		}
		c.post(t, depot, acme, seq, hoa, at)
	}
	archived := person(old, acme, minh, domain.RoleMember, 90)
	archived.Preferences.ArchivedAt = day.AddDate(0, 0, -1)
	c.create(t, group(old, acme, "Cũ"), person(old, acme, lan, domain.RoleOwner, 90), archived)
	for seq := uint64(81); seq <= 90; seq++ {
		c.post(t, old, acme, seq, lan, day.AddDate(0, 0, -7).Add(time.Duration(seq)*time.Minute))
	}
}

func seedNoise(t *testing.T, c *countingStore) {
	t.Helper()
	c.create(t, group(left, acme, "Rời"), person(left, acme, lan, domain.RoleOwner, 0), person(left, acme, minh, domain.RoleMember, 0))
	c.post(t, left, acme, 1, lan, clock(11, 0))
	c.leave(t, left, minh)
	c.create(t, group(foreign, globex, "Ngoài"), person(foreign, globex, minh, domain.RoleOwner, 0), person(foreign, globex, lan, domain.RoleMember, 0))
	c.post(t, foreign, globex, 1, lan, clock(11, 30))
}
