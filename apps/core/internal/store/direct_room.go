package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type DirectRooms interface {
	Claim(ctx context.Context, tenant, a, b string, candidate uint64, at time.Time) (uint64, error)
	Repoint(ctx context.Context, tenant, a, b string, old, next uint64) (bool, error)
}

func ValidateDirectPair(tenant, a, b string) error {
	switch {
	case domain.ValidTenant(tenant) != nil:
		return invalid("tenant")
	case domain.ValidUser(a) != nil || domain.ValidUser(b) != nil:
		return invalid("user")
	case a == b:
		return invalid("direct pair")
	}
	return nil
}

func ValidateDirectClaim(tenant, a, b string, candidate uint64, at time.Time) error {
	if err := ValidateDirectPair(tenant, a, b); err != nil {
		return err
	}
	switch {
	case candidate == 0:
		return invalid("room id")
	case at.IsZero():
		return invalid("time")
	}
	return nil
}

func ValidateDirectRepoint(tenant, a, b string, next uint64) error {
	if err := ValidateDirectPair(tenant, a, b); err != nil {
		return err
	}
	if next == 0 {
		return invalid("room id")
	}
	return nil
}

func ValidateRoomInsert(r domain.Room) error {
	switch {
	case r.ID == 0:
		return invalid("room id")
	case domain.ValidTenant(r.Tenant) != nil:
		return invalid("tenant")
	case r.Type == domain.RoomGroup && r.DMKey != "":
		return invalid("dm key")
	case r.Type == domain.RoomDM && r.DMKey == "":
		return invalid("dm key")
	case r.Type != domain.RoomGroup && r.Type != domain.RoomDM:
		return invalid("type")
	}
	return nil
}
