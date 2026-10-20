package mongostore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var ownedCollections = []string{"mention_groups", "mention_rooms", "mention_user_groups", "mentions"}

func TestModuleOpensOnlyOwnedCollections(t *testing.T) {
	var names []string
	for path, file := range moduleSources(t, filepath.Join("..", "..", "..")) {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if opensLiteralCollection(node) {
					t.Errorf("%s: collection opened by a literal name, use a named constant", path)
				}
			case *ast.ValueSpec:
				names = append(names, collectionNames(t, node)...)
			}
			return true
		})
	}
	slices.Sort(names)
	if !slices.Equal(names, ownedCollections) {
		t.Fatalf("collection constants = %v, want only %v", names, ownedCollections)
	}
}

func moduleSources(t *testing.T, root string) map[string]*ast.File {
	t.Helper()
	files := map[string]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		files[path] = file
		return err
	})
	if err != nil {
		t.Fatalf("parse module sources: %v", err)
	}
	return files
}

func opensLiteralCollection(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Collection" && sel.Sel.Name != "CreateCollection") {
		return false
	}
	return slices.ContainsFunc(call.Args, func(arg ast.Expr) bool {
		lit, ok := arg.(*ast.BasicLit)
		return ok && lit.Kind == token.STRING
	})
}

func collectionNames(t *testing.T, spec *ast.ValueSpec) []string {
	t.Helper()
	var out []string
	for i, name := range spec.Names {
		if !strings.HasSuffix(name.Name, "Collection") || i >= len(spec.Values) {
			continue
		}
		lit, ok := spec.Values[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", name.Name, err)
		}
		out = append(out, value)
	}
	return out
}
