package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const maxOwnersInView = 2

type ownersVerDoc struct {
	OwnersVer int64 `bson:"owners_ver"`
}

func (s *Store) ownerView(ctx context.Context, key int64, room uint64, users []string) (store.OwnerView, error) {
	var d ownersVerDoc
	onlyVer := options.FindOne().SetProjection(bson.D{{Key: "owners_ver", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, onlyVer); err != nil {
		return store.OwnerView{}, fmt.Errorf("change owners of room %d: %w", room, err)
	}
	ownersVer, err := toUint64("owners ver", d.OwnersVer)
	if err != nil {
		return store.OwnerView{}, err
	}
	docs, err := membersOf(ctx, s.members, room, users)
	if err != nil {
		return store.OwnerView{}, err
	}
	byJoin := options.Find().SetSort(bson.D{{Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1}}).SetLimit(maxOwnersInView)
	owners, err := findMembers(ctx, s.members, activeRole(key, domain.RoleOwner), byJoin)
	if err != nil {
		return store.OwnerView{}, fmt.Errorf("owners of room %d: %w", room, err)
	}
	v := store.OwnerView{OwnersVer: ownersVer, Docs: docs, Owners: owners}
	top := options.Find().SetSort(bson.D{{Key: "priority", Value: -1}, {Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1}}).SetLimit(1)
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		first, err := findMembers(ctx, s.members, activeRole(key, role), top)
		if err != nil {
			return store.OwnerView{}, fmt.Errorf("top %s of room %d: %w", role, room, err)
		}
		v.Candidates = append(v.Candidates, first...)
	}
	return v, nil
}

func activeRole(room int64, role domain.Role) bson.D {
	return bson.D{{Key: "room_id", Value: room}, {Key: "state", Value: int64(domain.MemberActive)}, {Key: "role", Value: role}}
}
