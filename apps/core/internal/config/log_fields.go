package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
)

var (
	durationType  = reflect.TypeFor[time.Duration]()
	unloggedKinds = []reflect.Kind{reflect.Func, reflect.Chan, reflect.Interface, reflect.Pointer, reflect.UnsafePointer}
)

func (c Config) attrs(v reflect.Value, prefix string) []slog.Attr {
	t := v.Type()
	out := make([]slog.Attr, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		path := prefix + f.Name
		if secret, ok := secretFields[path]; ok {
			out = append(out, secret(c))
			continue
		}
		if !loggable(f) {
			continue
		}
		out = append(out, c.attr(snakeCase(f.Name), v.Field(i), path))
	}
	return out
}

func loggable(f reflect.StructField) bool {
	return f.IsExported() && !slices.Contains(unloggedKinds, f.Type.Kind())
}

func (c Config) attr(key string, v reflect.Value, path string) slog.Attr {
	if v.Type() == durationType {
		return slog.Duration(key, time.Duration(v.Int()))
	}
	switch v.Kind() {
	case reflect.Struct:
		return slog.Attr{Key: key, Value: slog.GroupValue(c.attrs(v, path+".")...)}
	case reflect.Slice, reflect.Array:
		items := make([]string, v.Len())
		for i := range items {
			items[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return slog.String(key, strings.Join(items, ","))
	default:
		return slog.Any(key, v.Interface())
	}
}

func snakeCase(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if !unicode.IsUpper(prev) || nextLower {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
