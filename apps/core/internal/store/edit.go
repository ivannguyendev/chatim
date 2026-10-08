package store

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MaxEditPage = 100
	MaxEditScan = 1000
)

var (
	ErrEditExists   = fmt.Errorf("edit version %w", apperr.ErrAlreadyExists)
	ErrEditNotFound = fmt.Errorf("edit %w", apperr.ErrNotFound)
)

func EditKeyOf(e domain.Edit) MsgKey {
	return MsgKey{Room: e.Room, Thread: e.Thread, Seq: e.Seq}
}

func ValidateEdit(e domain.Edit) error {
	if err := EditKeyOf(e).Validate(); err != nil {
		return err
	}
	switch {
	case e.Version > math.MaxInt32:
		return invalid("version")
	case e.Kind != domain.EditText && e.Kind != domain.EditDelete && e.Kind != domain.EditOriginal:
		return invalid("edit kind")
	case (e.Version == 0) != (e.Kind == domain.EditOriginal):
		return invalid("version")
	default:
		return nil
	}
}

func ValidateProjectedEdit(e domain.Edit) error {
	if err := ValidateEdit(e); err != nil {
		return err
	}
	if e.Kind == domain.EditOriginal {
		return invalid("edit kind")
	}
	return nil
}

func ValidateLimit(limit, maxLimit int) error {
	if limit < 1 || limit > maxLimit {
		return invalid("limit")
	}
	return nil
}
