package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func plan() domain.Plan {
	at := time.UnixMilli(1000)
	return domain.Plan{
		Key:     domain.MessageKey{Room: 777, Seq: 41},
		Tenant:  "t1",
		Sender:  "Lan",
		Ver:     1,
		Live:    []domain.Target{{Kind: domain.TargetUser, ID: "Minh", At: at}, {Kind: domain.TargetAll, At: at}},
		Retired: []domain.Target{{Kind: domain.TargetGroup, ID: "team-sale", At: at}},
	}
}

func TestPlanValidate(t *testing.T) {
	if err := plan().Validate(); err != nil {
		t.Fatalf("validate plan: %v", err)
	}
	empty := plan()
	empty.Live, empty.Retired = nil, nil
	if err := empty.Validate(); err != nil {
		t.Fatalf("validate empty plan: %v", err)
	}
}

func TestPlanValidateRejects(t *testing.T) {
	cases := map[string]func(*domain.Plan){
		"bad key":             func(p *domain.Plan) { p.Key.Seq = 0 },
		"bad tenant":          func(p *domain.Plan) { p.Tenant = "T 1" },
		"bad sender":          func(p *domain.Plan) { p.Sender = "" },
		"bad live target":     func(p *domain.Plan) { p.Live[0].ID = "a b" },
		"bad retired target":  func(p *domain.Plan) { p.Retired[0].Kind = 0 },
		"duplicate live":      func(p *domain.Plan) { p.Live = append(p.Live, domain.Target{Kind: domain.TargetUser, ID: "Minh"}) },
		"live and retired":    func(p *domain.Plan) { p.Retired = append(p.Retired, domain.Target{Kind: domain.TargetAll}) },
		"duplicate retired":   func(p *domain.Plan) { p.Retired = append(p.Retired, p.Retired[0]) },
		"live without a time": func(p *domain.Plan) { p.Live[0].At = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := plan()
			mutate(&p)
			if err := p.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("validate = %v, want invalid argument", err)
			}
		})
	}
}

func TestMentionStates(t *testing.T) {
	if domain.StateLive != 1 || domain.StateRetired != 2 {
		t.Fatalf("states = %d, %d", domain.StateLive, domain.StateRetired)
	}
}

func TestPlanInMillisTruncatesEveryTargetTime(t *testing.T) {
	p := plan()
	p.Live[0].At = time.UnixMilli(1000).Add(999_999)
	p.Retired[0].At = time.Time{}
	got := p.InMillis()
	if !got.Live[0].At.Equal(time.UnixMilli(1000)) || !got.Live[1].At.Equal(time.UnixMilli(1000)) || !got.Retired[0].At.IsZero() {
		t.Fatalf("InMillis = %+v", got)
	}
	if p.Live[0].At.Equal(time.UnixMilli(1000)) {
		t.Fatalf("InMillis changed the plan it was called on")
	}
}
