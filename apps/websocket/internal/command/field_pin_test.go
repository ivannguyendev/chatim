package command_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

type fieldPin struct {
	blocks map[protoreflect.FullName][]string
}

func (p *fieldPin) message(md protoreflect.MessageDescriptor) {
	if _, seen := p.blocks[md.FullName()]; seen {
		return
	}
	p.blocks[md.FullName()] = []string{}
	if md.Fields().Len() == 0 {
		p.add(md.FullName(), fmt.Sprintf("%s {}", md.FullName()))
	}
	for i := range md.Fields().Len() {
		f := md.Fields().Get(i)
		line := fmt.Sprintf("%s %d %s %s %s", md.FullName(), f.Number(), f.Name(), cardinality(f), kindOf(f))
		if oneof := f.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
			line += " oneof " + string(oneof.Name())
		}
		p.add(md.FullName(), line)
		switch {
		case f.Message() != nil:
			p.message(f.Message())
		case f.Enum() != nil:
			p.enum(f.Enum())
		}
	}
}

func (p *fieldPin) enum(ed protoreflect.EnumDescriptor) {
	if _, seen := p.blocks[ed.FullName()]; seen {
		return
	}
	p.blocks[ed.FullName()] = []string{}
	for i := range ed.Values().Len() {
		v := ed.Values().Get(i)
		p.add(ed.FullName(), fmt.Sprintf("%s = %d %s", ed.FullName(), v.Number(), v.Name()))
	}
}

func (p *fieldPin) add(name protoreflect.FullName, line string) {
	p.blocks[name] = append(p.blocks[name], line)
}

func cardinality(f protoreflect.FieldDescriptor) string {
	switch {
	case f.IsMap():
		return "map"
	case f.IsList():
		return "repeated"
	case f.HasOptionalKeyword():
		return "optional"
	}
	return "single"
}

func kindOf(f protoreflect.FieldDescriptor) string {
	switch {
	case f.Message() != nil:
		return "message " + string(f.Message().FullName())
	case f.Enum() != nil:
		return "enum " + string(f.Enum().FullName())
	}
	return f.Kind().String()
}

func reachableFields() string {
	p := &fieldPin{blocks: map[protoreflect.FullName][]string{}}
	for _, field := range requestFields() {
		p.message(field.Message())
	}
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(p.blocks)) {
		lines = append(lines, p.blocks[name]...)
	}
	return strings.Join(lines, "\n")
}

func TestClientReachableFieldsArePinned(t *testing.T) {
	got := reachableFields()
	if got != strings.TrimSpace(approvedClientFields) {
		t.Fatalf("fields a client can send changed; review each new field, message or enum value and decide whether clients may use it, then update approvedClientFields.\ngot:\n%s", got)
	}
}
