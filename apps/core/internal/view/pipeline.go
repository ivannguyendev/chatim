package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type Viewer struct {
	User             string
	Room             domain.Room
	ClearedBeforeSeq uint64
	HiddenSeqs       map[uint64]bool
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
