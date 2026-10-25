package mongoindex

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Collection struct {
	Name    string
	Indexes []mongo.IndexModel
}

type Replacement struct {
	Collection string
	Old        string
	OldKeys    bson.D
	New        string
}

func (r Replacement) Retired() bool { return r.New == "" }

type Catalog struct {
	Collections  []Collection
	Replacements []Replacement
}

func (c Catalog) With(other Catalog) Catalog {
	return Catalog{
		Collections:  append(slices.Clone(c.Collections), other.Collections...),
		Replacements: append(slices.Clone(c.Replacements), other.Replacements...),
	}
}

func (c Catalog) Names() []string {
	out := make([]string, len(c.Collections))
	for i, coll := range c.Collections {
		out[i] = coll.Name
	}
	return out
}

func (c Catalog) replacementsOf(coll string) []Replacement {
	var out []Replacement
	for _, r := range c.Replacements {
		if r.Collection == coll {
			out = append(out, r)
		}
	}
	return out
}

func Name(m mongo.IndexModel) (string, error) {
	o, err := resolveOptions(m)
	if err != nil {
		return "", err
	}
	if o.Name != nil {
		return *o.Name, nil
	}
	parts, err := keyParts(m.Keys)
	if err != nil {
		return "", err
	}
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.field + "_" + p.direction
	}
	return strings.Join(names, "_"), nil
}

func Pattern(keys any) (string, error) {
	parts, err := keyParts(keys)
	if err != nil {
		return "", err
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.field + ":" + p.direction
	}
	return strings.Join(out, ","), nil
}

type keyPart struct{ field, direction string }

func keyParts(keys any) ([]keyPart, error) {
	raw, ok := keys.(bson.Raw)
	if !ok {
		b, err := bson.Marshal(keys)
		if err != nil {
			return nil, fmt.Errorf("index keys: %w", err)
		}
		raw = b
	}
	elems, err := raw.Elements()
	if err != nil {
		return nil, fmt.Errorf("index keys: %w", err)
	}
	out := make([]keyPart, len(elems))
	for i, e := range elems {
		dir, err := direction(e.Value())
		if err != nil {
			return nil, fmt.Errorf("index key %s: %w", e.Key(), err)
		}
		out[i] = keyPart{field: e.Key(), direction: dir}
	}
	return out, nil
}

func direction(v bson.RawValue) (string, error) {
	switch v.Type {
	case bson.TypeInt32:
		return strconv.FormatInt(int64(v.Int32()), 10), nil
	case bson.TypeInt64:
		return strconv.FormatInt(v.Int64(), 10), nil
	case bson.TypeDouble:
		return strconv.FormatFloat(v.Double(), 'f', -1, 64), nil
	case bson.TypeString:
		return v.StringValue(), nil
	default:
		return "", fmt.Errorf("unsupported direction type %s", v.Type)
	}
}
