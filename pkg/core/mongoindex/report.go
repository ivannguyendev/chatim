package mongoindex

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Report struct {
	Collection string
	Absent     bool
	Missing    []mongo.IndexModel
	Building   []string
	Conflicts  []Conflict
	Outdated   []Outdated
	Unknown    []string
}

func (r Report) Stale() bool {
	return len(r.Missing) > 0 || len(r.Outdated) > 0 || len(r.Conflicts) > 0
}

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

func (r Report) ConflictNames() []string {
	out := make([]string, len(r.Conflicts))
	for i, c := range r.Conflicts {
		out[i] = c.Name
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
	for _, name := range r.Building {
		out = append(out, r.Collection+" building "+name)
	}
	for _, c := range r.Conflicts {
		out = append(out, r.Collection+" conflict "+c.Name+": "+c.Detail)
	}
	for _, o := range r.Outdated {
		switch o.State {
		case ReplacementRetired:
			out = append(out, r.Collection+" retired "+o.Old)
		case ReplacementOldDiffers:
			out = append(out, r.Collection+" outdated "+o.Old+" has other keys than declared, left alone")
		default:
			out = append(out, fmt.Sprintf("%s outdated %s replaced by %s (%s)", r.Collection, o.Old, o.New, o.State))
		}
	}
	for _, name := range r.Unknown {
		out = append(out, r.Collection+" unknown "+name)
	}
	return out
}
