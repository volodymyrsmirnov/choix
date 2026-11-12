package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// eventTypes is the payload each event is registered with. Wails matches the
// emitted value's type exactly, so these are the types the runtime emits by
// value.
var eventTypes = map[string]reflect.Type{
	events.LibraryChanged: reflect.TypeFor[events.LibraryChangedEvent](),
	events.ScanProgress:   reflect.TypeFor[events.ScanProgressEvent](),
	events.ScanState:      reflect.TypeFor[events.ScanStateEvent](),
	events.QualityStatus:  reflect.TypeFor[events.Quality](),
	events.FolderChanged:  reflect.TypeFor[events.FolderChangedEvent](),
	events.MenuAction:     reflect.TypeFor[events.MenuActionEvent](),
}

// eventConsts maps each string constant declared in internal/app/events to
// its value.
func eventConsts(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../../internal/app/events/events.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out[name.Name] = v
			}
		}
	}
	return out
}

// TestEvents_EveryNameRegistered reads events.go: its one init must hold
// exactly one direct RegisterEvent call per name in events.Names(), each
// with that event's payload type, so the binding generator sees them all.
func TestEvents_EveryNameRegistered(t *testing.T) {
	consts := eventConsts(t)
	f, err := parser.ParseFile(token.NewFileSet(), "events.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}

	inits := 0
	registered := map[string]string{} // event name → payload type, as written
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "init" || fd.Recv != nil {
			continue
		}
		inits++
		for _, stmt := range fd.Body.List {
			es, ok := stmt.(*ast.ExprStmt)
			if !ok {
				t.Errorf("init: unexpected statement %T; want only RegisterEvent calls", stmt)
				continue
			}
			name, typ, err := registerCall(es.X, consts)
			if err != nil {
				t.Error(err)
				continue
			}
			if prev, dup := registered[name]; dup {
				t.Errorf("%q registered twice (%s, %s)", name, prev, typ)
			}
			registered[name] = typ
		}
	}
	if inits != 1 {
		t.Fatalf("events.go has %d init functions; want 1", inits)
	}

	for _, name := range events.Names() {
		typ, ok := registered[name]
		if !ok {
			t.Errorf("event %q is not registered", name)
			continue
		}
		want := eventTypes[name]
		if want == nil {
			t.Errorf("event %q has no expected payload type in this test", name)
			continue
		}
		if got := "events." + want.Name(); typ != got {
			t.Errorf("event %q registered with %s; want %s", name, typ, got)
		}
		delete(registered, name)
	}
	for name := range registered {
		t.Errorf("events.go registers %q, which is not in events.Names()", name)
	}
	if n := len(events.Names()); n != 6 {
		t.Errorf("events.Names() has %d names; the shell registers 6", n)
	}
}

// registerCall returns the event name and the payload type expression of a
// call application.RegisterEvent[events.T](events.Name).
func registerCall(x ast.Expr, consts map[string]string) (name, typ string, err error) {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		return "", "", fmt.Errorf("init: %T is not a call", x)
	}
	idx, ok := call.Fun.(*ast.IndexExpr)
	if !ok {
		return "", "", fmt.Errorf("init: call is not an instantiated generic function")
	}
	if fn := selector(idx.X); fn != "application.RegisterEvent" {
		return "", "", fmt.Errorf("init: call to %s; want application.RegisterEvent", fn)
	}
	typ = selector(idx.Index)
	if !strings.HasPrefix(typ, "events.") {
		return "", "", fmt.Errorf("init: payload type %s is not an events type (pointers are not allowed)", typ)
	}
	if len(call.Args) != 1 {
		return "", "", fmt.Errorf("init: RegisterEvent with %d arguments", len(call.Args))
	}
	arg := selector(call.Args[0])
	id, ok := strings.CutPrefix(arg, "events.")
	if !ok {
		return "", "", fmt.Errorf("init: event name %s is not an events constant", arg)
	}
	name, ok = consts[id]
	if !ok {
		return "", "", fmt.Errorf("init: events.%s is not a string constant", id)
	}
	return name, typ, nil
}

// selector renders pkg.Name, or "" for any other expression.
func selector(x ast.Expr) string {
	sel, ok := x.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// TestEvents_RegisteredAtInit checks the registrations really ran: Wails
// refuses to register a name twice and names the type it already has.
func TestEvents_RegisteredAtInit(t *testing.T) {
	for _, name := range events.Names() {
		t.Run(name, func(t *testing.T) {
			msg := registerAgain(name)
			if msg == "" {
				t.Fatalf("%q was not registered at init", name)
			}
			if want := eventTypes[name]; want == nil || !strings.HasSuffix(msg, " "+want.String()) {
				t.Errorf("%q: %s; want it registered with %v", name, msg, want)
			}
		})
	}
}

// registerAgain registers name once more and returns Wails' panic message,
// or "" when it did not panic.
func registerAgain(name string) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	application.RegisterEvent[struct{}](name)
	return ""
}
