package mongostore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	itRealm   = "api.shop"
	itSub     = "0901000000"
	itLinkKey = "shop│api.shop│0901000000"
)

type linkRow struct {
	TenantID  string    `bson:"tenant_id"`
	Realm     string    `bson:"realm"`
	Sub       string    `bson:"sub"`
	UserID    string    `bson:"user_id"`
	CreatedAt time.Time `bson:"created_at"`
	CreatedBy string    `bson:"created_by"`
}

type linkedUserRow struct {
	TenantID    string      `bson:"tenant_id"`
	ID          string      `bson:"id"`
	DisplayName string      `bson:"display_name"`
	State       store.State `bson:"state"`
	Groups      []string    `bson:"groups"`
	Ver         int64       `bson:"ver"`
	Realm       string      `bson:"realm"`
	Sub         string      `bson:"sub"`
	CreatedAt   time.Time   `bson:"created_at"`
	UpdatedBy   string      `bson:"updated_by"`
}

type linkOutcome struct {
	linked store.LinkedUser
	err    error
}

func itNewUser(candidate string) store.NewUser {
	return store.NewUser{TenantID: "shop", Realm: itRealm, Sub: itSub, CandidateID: candidate, DisplayName: "Lan", At: itAt, By: jitBy}
}

func mustLinkUser(t *testing.T, s *Store, u store.NewUser) store.LinkedUser {
	t.Helper()
	linked, err := s.LinkUser(t.Context(), u)
	if err != nil {
		t.Fatalf("LinkUser: %v", err)
	}
	return linked
}

func readRow[T any](t *testing.T, s *Store, collection, id string) T {
	t.Helper()
	var row T
	if err := s.db.Collection(collection).FindOne(t.Context(), bson.D{{Key: "_id", Value: id}}).Decode(&row); err != nil {
		t.Fatalf("read %s %s: %v", collection, id, err)
	}
	return row
}

func wantLinkedUser(t *testing.T, s *Store, id string) {
	t.Helper()
	want := linkedUserRow{
		TenantID: "shop", ID: id, DisplayName: "Lan", State: store.StateActive, Groups: []string{}, Ver: 1,
		Realm: itRealm, Sub: itSub, CreatedAt: itAt, UpdatedBy: jitBy,
	}
	if got := readRow[linkedUserRow](t, s, usersCollection, tenantKey("shop", id)); !sameLinkedUser(got, want) {
		t.Fatalf("user = %+v; want %+v", got, want)
	}
	if n := countUsers(t, s); n != 1 {
		t.Fatalf("users = %d; want 1", n)
	}
}

func sameLinkedUser(a, b linkedUserRow) bool {
	return slices.Equal(a.Groups, b.Groups) && a.CreatedAt.Equal(b.CreatedAt) &&
		a.TenantID == b.TenantID && a.ID == b.ID && a.DisplayName == b.DisplayName && a.State == b.State &&
		a.Ver == b.Ver && a.Realm == b.Realm && a.Sub == b.Sub && a.UpdatedBy == b.UpdatedBy
}

func TestLinkUserCreatesTheLinkAndTheUser(t *testing.T) {
	s := testDatabase(t)
	if got := mustLinkUser(t, s, itNewUser("k7q2m9")); got != (store.LinkedUser{ID: "k7q2m9", Result: store.LinkCreated}) {
		t.Fatalf("LinkUser = %+v; want k7q2m9 created", got)
	}
	link := readRow[linkRow](t, s, userLinksCollection, itLinkKey)
	if link.TenantID != "shop" || link.Realm != itRealm || link.Sub != itSub || link.UserID != "k7q2m9" ||
		!link.CreatedAt.Equal(itAt) || link.CreatedBy != jitBy {
		t.Fatalf("link = %+v; want shop %s %s -> k7q2m9 at %v by %s", link, itRealm, itSub, itAt, jitBy)
	}
	wantLinkedUser(t, s, "k7q2m9")
	if got := mustLinkUser(t, s, itNewUser("other")); got != (store.LinkedUser{ID: "k7q2m9", Result: store.LinkExisting}) {
		t.Fatalf("second LinkUser = %+v; want k7q2m9 existing", got)
	}
}

func TestLinkUserHealsALinkWithoutItsUser(t *testing.T) {
	s := testDatabase(t)
	insertDoc(t, s, userLinksCollection, newLinkDoc(itLinkKey, itNewUser("winner")))
	if got := mustLinkUser(t, s, itNewUser("loser")); got != (store.LinkedUser{ID: "winner", Result: store.LinkHealed}) {
		t.Fatalf("LinkUser = %+v; want winner healed", got)
	}
	wantLinkedUser(t, s, "winner")
}

func TestLinkUserRacingReplicasAgreeOnOneID(t *testing.T) {
	s := testDatabase(t)
	other, err := Open(testOptions(itURI(t), s.db.Name()))
	if err != nil {
		t.Fatalf("Open second replica: %v", err)
	}
	t.Cleanup(other.Close)
	start := make(chan struct{})
	out := make(chan linkOutcome, 2)
	for _, r := range []struct {
		s    *Store
		cand string
	}{{s, "cand-a"}, {other, "cand-b"}} {
		go func() {
			<-start
			linked, err := r.s.LinkUser(context.Background(), itNewUser(r.cand))
			out <- linkOutcome{linked, err}
		}()
	}
	close(start)
	got := []linkOutcome{waitFor(t, out, "racing link"), waitFor(t, out, "racing link")}
	for i, o := range got {
		if o.linked.Result == store.LinkPending && errors.Is(o.err, apperr.ErrUnavailable) {
			linked, err := s.LinkUser(t.Context(), itNewUser("cand-retry"))
			got[i] = linkOutcome{linked, err}
		}
	}
	created := 0
	for _, o := range got {
		if o.err != nil {
			t.Fatalf("racing LinkUser = %+v, %v", o.linked, o.err)
		}
		if o.linked.Result == store.LinkCreated {
			created++
		}
	}
	id := got[0].linked.ID
	if created != 1 || got[1].linked.ID != id || (id != "cand-a" && id != "cand-b") {
		t.Fatalf("racing results = %+v; want one created and one shared id", got)
	}
	if link := readRow[linkRow](t, s, userLinksCollection, itLinkKey); link.UserID != id {
		t.Fatalf("link user_id = %q; want %q", link.UserID, id)
	}
	wantLinkedUser(t, s, id)
}

func TestLinkReadUsesTheClusteredIndex(t *testing.T) {
	s := testDatabase(t)
	insertDoc(t, s, userLinksCollection, newLinkDoc(itLinkKey, itNewUser("winner")))
	insertDoc(t, s, userLinksCollection, newLinkDoc("shop│api.shop│other", itNewUser("other")))
	find := bson.D{
		{Key: "find", Value: userLinksCollection}, {Key: "filter", Value: linkFilter(itLinkKey)},
		{Key: "projection", Value: linkProjection}, {Key: "limit", Value: 1}, {Key: "singleBatch", Value: true},
	}
	raw, err := s.db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: find}, {Key: "verbosity", Value: "executionStats"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	plan := raw.Lookup("queryPlanner", "winningPlan").String()
	examined := raw.Lookup("executionStats", "totalDocsExamined").AsInt64()
	returned := raw.Lookup("executionStats", "nReturned").AsInt64()
	if strings.Contains(plan, "COLLSCAN") || examined > 1 || returned != 1 {
		t.Fatalf("link read plan %s examined %d returned %d; want a clustered _id lookup of one doc", plan, examined, returned)
	}
	if id, found, err := s.readLinkMajority(t.Context(), itLinkKey); err != nil || !found || id != "winner" {
		t.Fatalf("readLinkMajority = %q, %v, %v; want winner", id, found, err)
	}
	if id, found, err := s.readLinkMajority(t.Context(), "shop│api.shop│ghost"); err != nil || found || id != "" {
		t.Fatalf("readLinkMajority of a missing link = %q, %v, %v; want not found", id, found, err)
	}
}

func TestSchemaReportsAMissingUserLinksCollection(t *testing.T) {
	s := testDatabase(t)
	if err := s.db.Collection(userLinksCollection).Drop(t.Context()); err != nil {
		t.Fatalf("drop user_links: %v", err)
	}
	state, err := s.Schema(t.Context())
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	if want := []string{"collection user_links absent"}; !slices.Equal(state.Missing, want) {
		t.Fatalf("Missing = %q; want %q", state.Missing, want)
	}
}
