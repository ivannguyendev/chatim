package mongoindex

import (
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const clusteredIndexName = "_id_"

var (
	ErrReplacementNotReady = errors.New("mongoindex: replacement index missing or still building, or the old index keys differ")
	ErrIndexConflict       = errors.New("mongoindex: an index conflicts with its declaration, fix it by hand first")
)

type Existing struct {
	Name     string
	Keys     string
	Options  string
	Building bool
}

type ReplacementState string

const (
	ReplacementReady      ReplacementState = "ready"
	ReplacementBuilding   ReplacementState = "building"
	ReplacementAbsent     ReplacementState = "absent"
	ReplacementRetired    ReplacementState = "retired"
	ReplacementOldDiffers ReplacementState = "old keys differ"
)

type Outdated struct {
	Replacement
	State ReplacementState
}

type Conflict struct {
	Name   string
	Detail string
}

type declared struct {
	model         mongo.IndexModel
	name, keys    string
	optionsString string
}

func describe(m mongo.IndexModel) (declared, error) {
	name, err := Name(m)
	if err != nil {
		return declared{}, err
	}
	keys, err := Pattern(m.Keys)
	if err != nil {
		return declared{}, err
	}
	opts, err := declaredOptions(m)
	if err != nil {
		return declared{}, err
	}
	return declared{model: m, name: name, keys: keys, optionsString: opts}, nil
}

func Compare(c Collection, replacements []Replacement, existing []Existing) (Report, error) {
	r := Report{Collection: c.Name}
	byKeys := make(map[string]Existing, len(existing))
	byName := make(map[string]Existing, len(existing))
	for _, e := range existing {
		byKeys[e.Keys] = e
		byName[e.Name] = e
	}
	known := map[string]bool{clusteredIndexName: true}
	for _, m := range c.Indexes {
		d, err := describe(m)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", c.Name, err)
		}
		r.addDeclared(d, byKeys, byName, known)
	}
	for _, rep := range replacements {
		e, ok := byName[rep.Old]
		if !ok || known[rep.Old] {
			continue
		}
		known[rep.Old] = true
		state, err := stateOf(rep, e, byName)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", c.Name, err)
		}
		r.Outdated = append(r.Outdated, Outdated{Replacement: rep, State: state})
	}
	for _, e := range existing {
		if !known[e.Name] {
			r.Unknown = append(r.Unknown, e.Name)
		}
	}
	return r, nil
}

func (r *Report) addDeclared(d declared, byKeys, byName map[string]Existing, known map[string]bool) {
	if e, ok := byKeys[d.keys]; ok {
		known[e.Name] = true
		switch {
		case e.Options != d.optionsString:
			r.Conflicts = append(r.Conflicts, Conflict{Name: e.Name, Detail: fmt.Sprintf("declared %s with options [%s], found [%s]", d.name, d.optionsString, e.Options)})
		case e.Building:
			r.Building = append(r.Building, e.Name)
		}
		return
	}
	if e, ok := byName[d.name]; ok {
		known[e.Name] = true
		r.Conflicts = append(r.Conflicts, Conflict{Name: e.Name, Detail: fmt.Sprintf("same name, declared keys %s, found %s", d.keys, e.Keys)})
		return
	}
	r.Missing = append(r.Missing, d.model)
}

func stateOf(rep Replacement, old Existing, byName map[string]Existing) (ReplacementState, error) {
	oldKeys, err := Pattern(rep.OldKeys)
	if err != nil {
		return "", fmt.Errorf("replacement %s: %w", rep.Old, err)
	}
	if oldKeys != old.Keys {
		return ReplacementOldDiffers, nil
	}
	if rep.Retired() {
		return ReplacementRetired, nil
	}
	e, ok := byName[rep.New]
	switch {
	case !ok:
		return ReplacementAbsent, nil
	case e.Building:
		return ReplacementBuilding, nil
	default:
		return ReplacementReady, nil
	}
}

func conflictsOf(reports []Report) error {
	var found []string
	for _, r := range reports {
		for _, c := range r.Conflicts {
			found = append(found, r.Collection+" "+c.Name+": "+c.Detail)
		}
	}
	if len(found) > 0 {
		return fmt.Errorf("%w: %s", ErrIndexConflict, strings.Join(found, "; "))
	}
	return nil
}

func dropPlan(reports []Report) ([]Replacement, error) {
	if err := conflictsOf(reports); err != nil {
		return nil, err
	}
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
