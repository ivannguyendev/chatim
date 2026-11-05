package mongoindex

import (
	"fmt"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type indexOptions struct {
	unique, sparse, hidden bool
	partial, ttl, locale   string
}

func (o indexOptions) String() string {
	var parts []string
	for _, flag := range []struct {
		on   bool
		name string
	}{{o.unique, "unique"}, {o.sparse, "sparse"}, {o.hidden, "hidden"}} {
		if flag.on {
			parts = append(parts, flag.name)
		}
	}
	for _, kv := range []struct{ name, value string }{{"partialFilterExpression", o.partial}, {"expireAfterSeconds", o.ttl}, {"collation", o.locale}} {
		if kv.value != "" {
			parts = append(parts, kv.name+"="+kv.value)
		}
	}
	return strings.Join(parts, ",")
}

func resolveOptions(m mongo.IndexModel) (options.IndexOptions, error) {
	var o options.IndexOptions
	if m.Options == nil {
		return o, nil
	}
	for _, set := range m.Options.List() {
		if err := set(&o); err != nil {
			return o, fmt.Errorf("index options: %w", err)
		}
	}
	return o, nil
}

func declaredOptions(m mongo.IndexModel) (string, error) {
	o, err := resolveOptions(m)
	if err != nil {
		return "", err
	}
	out := indexOptions{unique: isTrue(o.Unique), sparse: isTrue(o.Sparse), hidden: isTrue(o.Hidden)}
	if o.PartialFilterExpression != nil {
		raw, err := bson.Marshal(o.PartialFilterExpression)
		if err != nil {
			return "", fmt.Errorf("partial filter expression: %w", err)
		}
		out.partial = bson.Raw(raw).String()
	}
	if o.ExpireAfterSeconds != nil {
		out.ttl = strconv.FormatInt(int64(*o.ExpireAfterSeconds), 10)
	}
	if o.Collation != nil {
		out.locale = o.Collation.Locale
	}
	return out.String(), nil
}

func existingOptions(spec bson.Raw) string {
	out := indexOptions{
		unique: rawTrue(spec.Lookup("unique")),
		sparse: rawTrue(spec.Lookup("sparse")),
		hidden: rawTrue(spec.Lookup("hidden")),
	}
	if doc, ok := spec.Lookup("partialFilterExpression").DocumentOK(); ok {
		out.partial = doc.String()
	}
	if ttl, ok := spec.Lookup("expireAfterSeconds").AsInt64OK(); ok {
		out.ttl = strconv.FormatInt(ttl, 10)
	}
	if locale, ok := spec.Lookup("collation", "locale").StringValueOK(); ok && locale != "simple" {
		out.locale = locale
	}
	return out.String()
}

func isTrue(b *bool) bool { return b != nil && *b }

func rawTrue(v bson.RawValue) bool {
	if b, ok := v.BooleanOK(); ok {
		return b
	}
	n, ok := v.AsInt64OK()
	return ok && n != 0
}
