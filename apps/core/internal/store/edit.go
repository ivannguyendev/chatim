package store

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
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
	case e.Version == 0 || e.Version > math.MaxInt32:
		return invalid("version")
	case e.Kind != domain.EditText && e.Kind != domain.EditDelete:
		return invalid("edit kind")
	case e.Prev != "" && (e.Kind != domain.EditText || e.Version != 1):
		return invalid("prev")
	default:
		return nil
	}
}

func ValidateLimit(limit, maxLimit int) error {
	if limit < 1 || limit > maxLimit {
		return invalid("limit")
	}
	return nil
}
