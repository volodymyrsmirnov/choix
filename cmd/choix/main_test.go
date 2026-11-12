package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
)

// TestMain_RegistersFourServices reads main.go: the app binds exactly the
// four services Library, Catalog, Settings and System, in that order, and
// mounts the media handler at /media. The model binding is gone (spec §8).
func TestMain_RegistersFourServices(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var services, withOptions []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || selector(call.Fun) != "app.RegisterService" || len(call.Args) != 1 {
			return true
		}
		inner, ok := call.Args[0].(*ast.CallExpr)
		if !ok || len(inner.Args) == 0 {
			t.Errorf("RegisterService with an argument that is not a constructor call")
			return true
		}
		field := selector(inner.Args[0])
		switch selector(inner.Fun) {
		case "application.NewService":
			services = append(services, field)
		case "application.NewServiceWithOptions":
			withOptions = append(withOptions, field)
		default:
			t.Errorf("RegisterService(%s(...)); want application.NewService or NewServiceWithOptions", selector(inner.Fun))
		}
		return true
	})
	if want := []string{"rt.Library", "rt.Catalog", "rt.Settings", "rt.System"}; !reflect.DeepEqual(services, want) {
		t.Errorf("services = %v; want %v", services, want)
	}
	if want := []string{"rt.Media"}; !reflect.DeepEqual(withOptions, want) {
		t.Errorf("services with options = %v; want %v (the /media route)", withOptions, want)
	}
}
