package mongoindex

import (
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const clusteredIndexName = "_id_"

var ErrReplacementNotReady = errors.New("mongoindex: replacement index missing or still building")

type Existing struct {
	Name     string
	Keys     string
	Building bool
}

type ReplacementState string

const (
	ReplacementReady    ReplacementState = "ready"
	ReplacementBuilding ReplacementState = "building"
	ReplacementAbsent   ReplacementState = "absent"
	ReplacementRetired  ReplacementState = "retired"
)

type Outdated struct {
	Replacement
	State ReplacementState
}

type Report struct {
	Collection string
	Absent     bool
	Missing    []mongo.IndexModel
	Outdated   []Outdated
	Unknown    []string
}

func (r Report) Stale() bool { return len(r.Missing) > 0 || len(r.Outdated) > 0 }

func (r Report) MissingNames() []string {
	out := make([]string, 0, len(r.Missing))
	for _, m := range r.Missing {
		name, err := Name(m)
		if err != nil {
			name = fmt.Sprintf("<%v>", err)
		}
		out = append(out, name)
	}
	return out
}

func (r Report) OutdatedNames() []string {
	out := make([]string, len(r.Outdated))
	for i, o := range r.Outdated {
		out[i] = o.Old
	}
	return out
}

func (r Report) Lines() []string {
	if r.Absent {
		return []string{r.Collection + " absent, core creates it at startup"}
	}
	var out []string
	for _, name := range r.MissingNames() {
		out = append(out, r.Collection+" missing "+name)
	}
	for _, o := range r.Outdated {
		if o.State == ReplacementRetired {
			out = append(out, r.Collection+" retired "+o.Old)
			continue
		}
		out = append(out, fmt.Sprintf("%s outdated %s replaced by %s (%s)", r.Collection, o.Old, o.New, o.State))
	}
	for _, name := range r.Unknown {
		out = append(out, r.Collection+" unknown "+name)
	}
	return out
}

func Compare(c Collection, replacements []Replacement, existing []Existing) (Report, error) {
	r := Report{Collection: c.Name}
	byKeys := make(map[string]string, len(existing))
	byName := make(map[string]Existing, len(existing))
	for _, e := range existing {
		byKeys[e.Keys] = e.Name
		byName[e.Name] = e
	}
	known := map[string]bool{clusteredIndexName: true}
	for _, m := range c.Indexes {
		p, err := Pattern(m.Keys)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", c.Name, err)
		}
		if name, ok := byKeys[p]; ok {
			known[name] = true
			continue
		}
		r.Missing = append(r.Missing, m)
	}
	for _, rep := range replacements {
		if _, ok := byName[rep.Old]; !ok || known[rep.Old] {
			continue
		}
		known[rep.Old] = true
		r.Outdated = append(r.Outdated, Outdated{Replacement: rep, State: stateOf(rep, byName)})
	}
	for _, e := range existing {
		if !known[e.Name] {
			r.Unknown = append(r.Unknown, e.Name)
		}
	}
	return r, nil
}

func stateOf(rep Replacement, byName map[string]Existing) ReplacementState {
	if rep.Retired() {
		return ReplacementRetired
	}
	e, ok := byName[rep.New]
	switch {
	case !ok:
		return ReplacementAbsent
	case e.Building:
		return ReplacementBuilding
	default:
		return ReplacementReady
	}
}

func dropPlan(reports []Report) ([]Replacement, error) {
	var plan []Replacement
	var blocked []string
	for _, r := range reports {
		for _, o := range r.Outdated {
			switch o.State {
			case ReplacementReady, ReplacementRetired:
				plan = append(plan, o.Replacement)
			default:
				blocked = append(blocked, fmt.Sprintf("%s %s (replacement %s %s)", r.Collection, o.Old, o.New, o.State))
			}
		}
	}
	if len(blocked) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrReplacementNotReady, strings.Join(blocked, "; "))
	}
	return plan, nil
}
