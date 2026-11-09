package mongostore

import (
	"cmp"
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var errResolveSubject = fmt.Errorf("%w: issuer must be set without %q and sub must be 1 to 255 printable ASCII bytes", apperr.ErrInvalidArgument, keySeparator)

func (s *Store) ResolveUser(ctx context.Context, issuer, sub string) (string, string, error) {
	if issuer == "" || checkKeyPart("issuer", issuer) != nil || !trust.ValidSubject(sub) {
		return "", "", errResolveSubject
	}
	var iss trustedIssuerDoc
	err := s.db.Collection(trustedIssuersCollection).FindOne(ctx, bson.D{{Key: "_id", Value: issuer}},
		options.FindOne().SetProjection(bson.D{{Key: "tenant_id", Value: 1}, {Key: "realm", Value: 1}})).Decode(&iss)
	if err != nil {
		return "", "", fmt.Errorf("trusted issuer: %w", s.mapErr(err))
	}
	var link struct {
		UserID string `bson:"user_id"`
	}
	key := linkKey(iss.TenantID, cmp.Or(iss.Realm, iss.Issuer), sub)
	err = s.db.Collection(userLinksCollection).FindOne(ctx, linkFilter(key), options.FindOne().SetProjection(linkProjection)).Decode(&link)
	if err != nil {
		return "", "", fmt.Errorf("user link: %w", s.mapErr(err))
	}
	return iss.TenantID, link.UserID, nil
}
