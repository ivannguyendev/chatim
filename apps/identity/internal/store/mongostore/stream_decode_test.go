package mongostore

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func rawEvent(t *testing.T, operation, collection string, full bson.D) bson.Raw {
	t.Helper()
	doc := bson.D{
		{Key: "_id", Value: bson.D{{Key: "_data", Value: "8263"}}},
		{Key: "operationType", Value: operation},
		{Key: "ns", Value: bson.D{{Key: "db", Value: "identity"}, {Key: "coll", Value: collection}}},
	}
	if full != nil {
		doc = append(doc, bson.E{Key: "fullDocument", Value: full})
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return raw
}

func userFull() bson.D {
	return bson.D{
		{Key: "_id", Value: "shop│u1"}, {Key: "tenant_id", Value: "shop"}, {Key: "id", Value: "u1"},
		{Key: "display_name", Value: "Lan"}, {Key: "avatar_url", Value: "https://img/lan.png"},
		{Key: "state", Value: int32(2)}, {Key: "ver", Value: int64(7)},
	}
}

func TestDecodeStreamEventUserCarriesTheDirectoryEntry(t *testing.T) {
	want := store.DirectoryEntry{TenantID: "shop", ID: "u1", DisplayName: "Lan", AvatarURL: "https://img/lan.png", State: store.StateDisabled, Ver: 7}
	for _, op := range []string{"insert", "update", "replace"} {
		ev, err := decodeStreamEvent(rawEvent(t, op, usersCollection, userFull()))
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if !slices.Equal(ev.Kinds, []store.EventKind{store.EventUser}) || ev.User != want {
			t.Fatalf("%s users = %+v; want EventUser with %+v", op, ev, want)
		}
	}
}

func TestDecodeStreamEventUserWithoutAFullDocumentIsSkipped(t *testing.T) {
	ev, err := decodeStreamEvent(rawEvent(t, "update", usersCollection, nil))
	if err != nil || len(ev.Kinds) != 0 {
		t.Fatalf("update without fullDocument = %+v, %v; want no kinds", ev, err)
	}
}

func TestDecodeStreamEventTrustAndRevocationCollections(t *testing.T) {
	cases := []struct {
		collection string
		want       []store.EventKind
	}{
		{tenantsCollection, []store.EventKind{store.EventTrust, store.EventRevocation}},
		{trustedIssuersCollection, []store.EventKind{store.EventTrust}},
		{trustedKeysCollection, []store.EventKind{store.EventTrust}},
		{revokedSessionsCollection, []store.EventKind{store.EventRevocation}},
		{groupsCollection, nil},
		{slotsCollection, nil},
	}
	for _, tc := range cases {
		for _, op := range []string{"insert", "update", "replace"} {
			ev, err := decodeStreamEvent(rawEvent(t, op, tc.collection, bson.D{{Key: "_id", Value: "x"}}))
			if err != nil {
				t.Fatalf("%s %s: %v", op, tc.collection, err)
			}
			if !slices.Equal(ev.Kinds, tc.want) || ev.User != (store.DirectoryEntry{}) {
				t.Fatalf("%s %s = %+v; want kinds %v and no user", op, tc.collection, ev, tc.want)
			}
		}
	}
}

func TestDecodeStreamEventLosesThePositionOnInvalidateDropAndRename(t *testing.T) {
	for _, op := range []string{"invalidate", "drop", "dropDatabase", "rename"} {
		_, err := decodeStreamEvent(rawEvent(t, op, usersCollection, nil))
		if !errors.Is(err, store.ErrStreamLost) {
			t.Fatalf("%s = %v; want ErrStreamLost", op, err)
		}
	}
}

func TestDecodeStreamEventRefusesAMalformedEvent(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "operationType", Value: int32(5)}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := decodeStreamEvent(raw); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("malformed = %v; want unavailable", err)
	}
}

func TestStreamErrMapsHistoryLostAndFatalToStreamLost(t *testing.T) {
	s := &Store{uri: "mongodb://identity:secret-pw@db:27017/chatim_identity"}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"history lost", mongo.CommandError{Code: 286, Name: "ChangeStreamHistoryLost"}, store.ErrStreamLost},
		{"fatal", fmt.Errorf("getMore: %w", mongo.CommandError{Code: 280, Name: "ChangeStreamFatalError"}), store.ErrStreamLost},
		{"other server error", mongo.CommandError{Code: 13, Name: "Unauthorized"}, apperr.ErrUnavailable},
		{"network", errors.New("dial mongodb://identity:secret-pw@db:27017/chatim_identity: refused"), apperr.ErrUnavailable},
		{"history lost with the uri", fmt.Errorf("mongodb://identity:secret-pw@db:27017/chatim_identity: %w", mongo.CommandError{Code: 286}), store.ErrStreamLost},
	}
	for _, tc := range cases {
		got := s.streamErr(tc.err)
		if !errors.Is(got, tc.want) {
			t.Fatalf("%s = %v; want %v", tc.name, got, tc.want)
		}
		if errors.Is(tc.want, store.ErrStreamLost) && errors.Is(got, apperr.ErrUnavailable) {
			t.Fatalf("%s = %v; want stream lost, not unavailable", tc.name, got)
		}
		if strings.Contains(got.Error(), "secret-pw") {
			t.Fatalf("%s text = %q; want the password hidden", tc.name, got)
		}
	}
}
