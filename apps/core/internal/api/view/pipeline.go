package view

import (
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type Viewer struct {
	User       string
	Room       domain.Room
	ClearedAt  time.Time
	HiddenSeqs map[uint64]bool
}

func (v Viewer) Cleared(createdAt time.Time) bool {
	return !v.ClearedAt.IsZero() && !createdAt.After(v.ClearedAt)
}

type Step func(v Viewer, msgs []domain.Message) []domain.Message

type Pipeline struct {
	steps []Step
}

func New(steps ...Step) Pipeline { return Pipeline{steps: slices.Clone(steps)} }

func Default() Pipeline { return New(CollapseRetried, MaskDeleted, HideForViewer) }

func (p Pipeline) Apply(v Viewer, msgs []domain.Message) []domain.Message {
	for _, step := range p.steps {
		msgs = step(v, msgs)
	}
	return msgs
}
