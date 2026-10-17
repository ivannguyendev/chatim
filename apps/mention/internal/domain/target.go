package domain

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type TargetKind uint8

const (
	TargetUser  TargetKind = 1
	TargetGroup TargetKind = 2
	TargetAll   TargetKind = 3
)

type Target struct {
	Kind TargetKind
	ID   string
	At   time.Time
}

func (t Target) Identity() Target { return Target{Kind: t.Kind, ID: t.ID} }

func (t Target) Name(room uint64) string {
	switch t.Kind {
	case TargetUser:
		return "user:" + t.ID
	case TargetGroup:
		return "group:" + t.ID
	case TargetAll:
		return "all:" + strconv.FormatUint(room, 10)
	default:
		return ""
	}
}

func (t Target) Validate() error {
	switch t.Kind {
	case TargetAll:
		if t.ID == "" {
			return nil
		}
	case TargetUser, TargetGroup:
		if keys.ValidMentionID(t.ID) {
			return nil
		}
	}
	return invalid("mention target")
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", apperr.ErrInvalidArgument, field)
}
