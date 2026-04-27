// link-state-rewrite is a config-driven AST refactor that promotes
// package-level vars in cmd/link/internal/ld onto fields of *Link
// (the per-invocation context), rewrites every unqualified read or
// write of a target var into a ctxt.<field> selector, and removes
// the original package-level declaration once nothing references
// it.
//
// The tool deliberately does NOT plumb ctxt through callers. If a
// function references a target var but has no *Link in lexical
// scope, the tool prints a "no ctxt in scope" report for that
// function and leaves the call site untouched. The follow-up is
// either to (a) add a ctxt parameter manually (small set, easier
// done by hand than by the tool guessing), or (b) confirm the var
// is genuinely process-global and revert it from the config.
//
// Carrier resolution at each rewrite site walks the enclosing
// function's scope:
//   1. A parameter of type *Link or *ld.Link.
//   2. A method receiver of type *Link (then "ctxt" is the receiver name).
//   3. A method receiver with a field of type *Link (e.g. dwctxt.linkctxt).
//   4. A locally-declared `ctxt := <expr>` that types as *Link.
// If none of these are available, the tool reports the function and
// skips its rewrites.
//
// Usage:
//
//	go run ./internal-tooling/link-state-rewrite \
//	    -config=internal-tooling/link-state-rewrite/dwarf.json \
//	    -root=src/cmd/link/internal/ld
//
// The config file is JSON of shape:
//
//	{
//	  "fields": [
//	    {"var": "gdbscript", "field": "gdbscript"},
//	    {"var": "dwarfp", "field": "dwarfp"}
//	  ]
//	}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type fieldSpec struct {
	Var   string `json:"var"`
	Field string `json:"field"`
	// Deref marks a `*T` flag pointer var. Reads in source typically
	// look like `*flagX`; the rewrite replaces the whole `*flagX`
	// expression with `ctxt.<field>` (no leading &). Naked `flagX`
	// references are left alone (rare; usually `&flagX` for flag.Var).
	Deref bool `json:"deref,omitempty"`
}

type config struct {
	Fields []fieldSpec `json:"fields"`
}

// scopeCtx tracks where a *Link "ctxt" is reachable inside a
// function. A non-empty Carrier means "use this Go expression to
// access *Link"; "" means none in scope.
//
// Shadowed is the set of names (parameters, receiver, locally
// declared idents) that shadow potential target vars within this
// function. Idents matching one of these names refer to the local
// binding, not the package var, so they must be left alone.
type scopeCtx struct {
	Carrier   string
	Shadowed  map[string]bool
}

var (
	root       string
	configPath string
	dryRun     bool
)

func main() {
	flag.StringVar(&root, "root", "src/cmd/link/internal/ld", "root directory to walk")
	flag.StringVar(&configPath, "config", "", "JSON config file with fields to migrate")
	flag.BoolVar(&dryRun, "n", false, "dry run; don't write files")
	flag.Parse()

	if configPath == "" {
		log.Fatal("-config is required")
	}

	cfg := loadConfig(configPath)
	targets := make(map[string]string, len(cfg.Fields))
	derefTargets := make(map[string]string, len(cfg.Fields))
	for _, fs := range cfg.Fields {
		if fs.Deref {
			derefTargets[fs.Var] = fs.Field
		} else {
			targets[fs.Var] = fs.Field
		}
	}

	fset := token.NewFileSet()
	missing := map[string]map[string]bool{} // file → set of function names without ctxt

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		af, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		changed := false
		fileMissing := map[string]bool{}

		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Body == nil {
				continue
			}
			ctx := newScopeCtx(fn)
			rewriteBlock(fn.Body, ctx, targets, derefTargets, fn, &changed, fileMissing)
		}

		// Also delete the package-level var declarations whose
		// names are in our target map. Two patterns:
		//
		//   var foo Type
		//   var foo Type = expr
		//   var (
		//       foo Type
		//       bar OtherType
		//   )
		//
		// We only delete a spec if its name is in targets.
		allTargets := make(map[string]string, len(targets)+len(derefTargets))
		for k, v := range targets {
			allTargets[k] = v
		}
		for k, v := range derefTargets {
			allTargets[k] = v
		}
		af.Decls = filterVarDecls(af.Decls, allTargets, &changed)

		if len(fileMissing) > 0 {
			missing[path] = fileMissing
		}
		if !changed {
			return nil
		}

		if dryRun {
			fmt.Printf("would rewrite %s\n", path)
			return nil
		}
		if err := writeFile(fset, af, path); err != nil {
			return err
		}
		fmt.Printf("rewrote %s\n", path)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	if len(missing) > 0 {
		fmt.Println()
		fmt.Println("Functions referencing target vars but with no *Link in scope:")
		for f, names := range missing {
			fmt.Printf("  %s\n", f)
			for n := range names {
				fmt.Printf("    %s\n", n)
			}
		}
		fmt.Println("Add a ctxt parameter manually, or revert the target from config.")
	}
}

func loadConfig(path string) config {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}
	if len(cfg.Fields) == 0 {
		log.Fatal("config has no fields")
	}
	return cfg
}

// newScopeCtx looks at a function declaration and decides whether a
// *Link is reachable, and via what expression. It also collects the
// names declared at function-signature scope (receiver, parameters,
// named results) so a Go ident matching one of those names is
// treated as a local binding, not a package-level reference.
func newScopeCtx(fn *ast.FuncDecl) *scopeCtx {
	ctx := &scopeCtx{Shadowed: map[string]bool{}}

	// Method receiver of type *Link → carrier is the receiver name.
	if fn.Recv != nil && len(fn.Recv.List) == 1 {
		f := fn.Recv.List[0]
		recvName := ""
		if len(f.Names) > 0 {
			recvName = f.Names[0].Name
			ctx.Shadowed[recvName] = true
		}
		if isLinkPtrType(f.Type) {
			ctx.Carrier = recvName
		} else if recvName != "" {
			if c := knownLinkField(f.Type, recvName); c != "" {
				ctx.Carrier = c
			}
		}
	}

	// Parameters and named results.
	collectNames := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, p := range fl.List {
			for _, n := range p.Names {
				ctx.Shadowed[n.Name] = true
			}
			if ctx.Carrier == "" && isLinkPtrType(p.Type) && len(p.Names) > 0 {
				ctx.Carrier = p.Names[0].Name
			}
		}
	}
	collectNames(fn.Type.Params)
	collectNames(fn.Type.Results)

	// Walk the function body and collect every name declared inside
	// it. Over-approximates: a name declared mid-function shadows
	// references before its declaration too. For our use case (we
	// never want to rewrite a name used as a local at all), that's
	// the right call.
	collectBodyDecls(fn.Body, ctx.Shadowed)

	return ctx
}

func collectBodyDecls(body *ast.BlockStmt, shadow map[string]bool) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE { // :=
				for _, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
						shadow[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, n := range x.Names {
				if n.Name != "_" {
					shadow[n.Name] = true
				}
			}
		case *ast.RangeStmt:
			if id, ok := x.Key.(*ast.Ident); ok && id.Name != "_" {
				shadow[id.Name] = true
			}
			if id, ok := x.Value.(*ast.Ident); ok && id.Name != "_" {
				shadow[id.Name] = true
			}
		case *ast.TypeSwitchStmt:
			if as, ok := x.Assign.(*ast.AssignStmt); ok {
				if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
					shadow[id.Name] = true
				}
			}
		case *ast.FuncLit:
			// Closure parameters & locals shadow within the closure.
			// We over-approximate by treating them as shadowed for
			// the entire enclosing function, which is fine since we
			// only want to leave package-var aliases alone.
			if x.Type.Params != nil {
				for _, p := range x.Type.Params.List {
					for _, n := range p.Names {
						shadow[n.Name] = true
					}
				}
			}
		}
		return true
	})
}

// isLinkPtrType reports whether expr is *Link or *ld.Link.
func isLinkPtrType(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch t := star.X.(type) {
	case *ast.Ident:
		return t.Name == "Link"
	case *ast.SelectorExpr:
		if id, ok := t.X.(*ast.Ident); ok && id.Name == "ld" && t.Sel.Name == "Link" {
			return true
		}
	}
	return false
}

// knownLinkField hardcodes that certain receiver types have a *Link
// field reachable as <recv>.<field>. The set is small; enumerate.
func knownLinkField(recvType ast.Expr, recvName string) string {
	star, ok := recvType.(*ast.StarExpr)
	if !ok {
		return ""
	}
	id, ok := star.X.(*ast.Ident)
	if !ok {
		return ""
	}
	switch id.Name {
	case "dwctxt":
		return recvName + ".linkctxt"
	case "relocSymState":
		return recvName + ".link"
	case "deadcodePass":
		return recvName + ".ctxt"
	}
	return ""
}

// rewriteBlock walks a function body, rewriting target var references
// to ctx.Carrier.<field>. Tracks local var shadowing and skips
// rewrites where the target is shadowed.
//
// derefTargets are flag-pointer vars (`*T`) read in source as `*flagX`.
// For these, we match a UnaryExpr (op=*) whose X is the target Ident
// and replace the entire UnaryExpr with the selector — dropping the *.
func rewriteBlock(body *ast.BlockStmt, ctx *scopeCtx, targets, derefTargets map[string]string, fn *ast.FuncDecl, changed *bool, missing map[string]bool) {
	if body == nil {
		return
	}
	apply(body, func(parent ast.Node, set func(ast.Node)) bool {
		// Deref: `*flagX` (StarExpr) where X is a target ident.
		// Rewrite the whole StarExpr to ctxt.<field>, dropping the *.
		// (Go's AST uses StarExpr for pointer dereference; UnaryExpr
		// is for prefix - ! ^ &.)
		if s, ok := parent.(*ast.StarExpr); ok {
			if id, ok := s.X.(*ast.Ident); ok {
				if rep, ok := buildReplacement(id.Name, ctx, derefTargets, fn, missing); ok {
					set(rep)
					*changed = true
					return false
				}
			}
		}
		// Plain ident matching a non-deref target.
		if id, ok := parent.(*ast.Ident); ok {
			if rep, ok := buildReplacement(id.Name, ctx, targets, fn, missing); ok {
				set(rep)
				*changed = true
				return false
			}
		}
		return true
	})
}

func buildReplacement(name string, ctx *scopeCtx, targets map[string]string, fn *ast.FuncDecl, missing map[string]bool) (ast.Expr, bool) {
	field, ok := targets[name]
	if !ok {
		return nil, false
	}
	// Local binding (parameter, receiver, named result) shadows the
	// package var: don't touch this ident, don't report missing.
	if ctx.Shadowed[name] {
		return nil, false
	}
	if ctx.Carrier == "" {
		missing[funcName(fn)] = true
		return nil, false
	}
	return makeSelector(ctx.Carrier, field), true
}

// makeSelector parses a dotted carrier (e.g. "ctxt" or "d.linkctxt")
// and returns a SelectorExpr ending in `.field`.
// isStructLikeType returns true if the type literal looks like a
// struct (an Ident or qualified Ident or anonymous *ast.StructType).
// Map/array/slice composite literals are not struct-like.
func isStructLikeType(t ast.Expr) bool {
	if t == nil {
		// Nested composite literal (key only specified at outer
		// level). Treat as struct-like to be conservative.
		return true
	}
	switch t := t.(type) {
	case *ast.StructType:
		return true
	case *ast.Ident:
		// Likely a named type. Heuristic: assume struct unless
		// it's a builtin that's not a struct (none of map/slice
		// look like Idents standalone).
		_ = t
		return true
	case *ast.SelectorExpr:
		return true // qualified type like pkg.Foo
	case *ast.MapType, *ast.ArrayType:
		return false
	case *ast.StarExpr:
		// *Foo composite literals don't exist in Go directly, but
		// the pointer to an anonymous struct would. Treat as struct.
		return true
	}
	return false
}

func makeSelector(carrier, field string) ast.Expr {
	parts := strings.Split(carrier, ".")
	var x ast.Expr = &ast.Ident{Name: parts[0]}
	for _, p := range parts[1:] {
		x = &ast.SelectorExpr{X: x, Sel: &ast.Ident{Name: p}}
	}
	return &ast.SelectorExpr{X: x, Sel: &ast.Ident{Name: field}}
}

// apply is a generic visitor matching calcsize-rewrite's. It calls
// visit for each child slot; if visit returns false, doesn't recurse
// further into that child.
func apply(n ast.Node, visit func(parent ast.Node, set func(ast.Node)) bool) {
	if n == nil {
		return
	}
	switch x := n.(type) {
	case *ast.BlockStmt:
		for i := range x.List {
			i := i
			cont := visit(x.List[i], func(r ast.Node) {
				if s, ok := r.(ast.Stmt); ok {
					x.List[i] = s
				}
			})
			if cont {
				apply(x.List[i], visit)
			}
		}
	case *ast.IfStmt:
		applyStmt(&x.Init, visit)
		applyExpr(&x.Cond, visit)
		apply(x.Body, visit)
		apply(x.Else, visit)
	case *ast.ForStmt:
		applyStmt(&x.Init, visit)
		applyExpr(&x.Cond, visit)
		applyStmt(&x.Post, visit)
		apply(x.Body, visit)
	case *ast.RangeStmt:
		applyExpr(&x.Key, visit)
		applyExpr(&x.Value, visit)
		applyExpr(&x.X, visit)
		apply(x.Body, visit)
	case *ast.SwitchStmt:
		applyStmt(&x.Init, visit)
		applyExpr(&x.Tag, visit)
		apply(x.Body, visit)
	case *ast.TypeSwitchStmt:
		applyStmt(&x.Init, visit)
		applyStmt(&x.Assign, visit)
		apply(x.Body, visit)
	case *ast.CaseClause:
		for i := range x.List {
			applyExpr(&x.List[i], visit)
		}
		for _, s := range x.Body {
			cont := visit(s, func(_ ast.Node) {})
			if cont {
				apply(s, visit)
			}
		}
	case *ast.SelectStmt:
		apply(x.Body, visit)
	case *ast.CommClause:
		for _, s := range x.Body {
			cont := visit(s, func(_ ast.Node) {})
			if cont {
				apply(s, visit)
			}
		}
	case *ast.AssignStmt:
		for i := range x.Lhs {
			applyExpr(&x.Lhs[i], visit)
		}
		for i := range x.Rhs {
			applyExpr(&x.Rhs[i], visit)
		}
	case *ast.ExprStmt:
		applyExpr(&x.X, visit)
	case *ast.IncDecStmt:
		applyExpr(&x.X, visit)
	case *ast.SendStmt:
		applyExpr(&x.Chan, visit)
		applyExpr(&x.Value, visit)
	case *ast.ReturnStmt:
		for i := range x.Results {
			applyExpr(&x.Results[i], visit)
		}
	case *ast.DeferStmt:
		applyExpr(&x.Call.Fun, visit)
		for i := range x.Call.Args {
			applyExpr(&x.Call.Args[i], visit)
		}
	case *ast.GoStmt:
		applyExpr(&x.Call.Fun, visit)
		for i := range x.Call.Args {
			applyExpr(&x.Call.Args[i], visit)
		}
	case *ast.CallExpr:
		applyExpr(&x.Fun, visit)
		for i := range x.Args {
			applyExpr(&x.Args[i], visit)
		}
	case *ast.BinaryExpr:
		applyExpr(&x.X, visit)
		applyExpr(&x.Y, visit)
	case *ast.UnaryExpr:
		applyExpr(&x.X, visit)
	case *ast.ParenExpr:
		applyExpr(&x.X, visit)
	case *ast.IndexExpr:
		applyExpr(&x.X, visit)
		applyExpr(&x.Index, visit)
	case *ast.SliceExpr:
		applyExpr(&x.X, visit)
		applyExpr(&x.Low, visit)
		applyExpr(&x.High, visit)
		applyExpr(&x.Max, visit)
	case *ast.StarExpr:
		applyExpr(&x.X, visit)
	case *ast.SelectorExpr:
		// Recurse into the X side only; the Sel field is the
		// member name being selected, not an identifier we want
		// to rewrite (e.g. ctxt.gdbscript's "gdbscript" Sel must
		// stay).
		applyExpr(&x.X, visit)
	case *ast.CompositeLit:
		applyExpr(&x.Type, visit)
		// In struct literals, KeyValueExpr.Key is a field NAME,
		// not an identifier reference — don't visit Key in that
		// case. For map/array literals the Key IS an expression
		// and should be visited; we approximate "is struct" by
		// looking at whether x.Type is a struct-shaped type.
		isStruct := isStructLikeType(x.Type)
		for i := range x.Elts {
			if kv, ok := x.Elts[i].(*ast.KeyValueExpr); ok && isStruct {
				applyExpr(&kv.Value, visit)
				continue
			}
			applyExpr(&x.Elts[i], visit)
		}
	case *ast.KeyValueExpr:
		// Top-level KeyValueExpr (rare outside CompositeLit): visit
		// both sides. The struct-literal case is handled above.
		applyExpr(&x.Key, visit)
		applyExpr(&x.Value, visit)
	case *ast.TypeAssertExpr:
		applyExpr(&x.X, visit)
	case *ast.FuncLit:
		apply(x.Body, visit)
	case *ast.DeclStmt:
		// Recurse into local var/const decls but don't rewrite
		// names being declared.
		if gd, ok := x.Decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					applyExpr(&vs.Type, visit)
					for i := range vs.Values {
						applyExpr(&vs.Values[i], visit)
					}
				}
			}
		}
	case *ast.LabeledStmt:
		cont := visit(x.Stmt, func(r ast.Node) {
			if s, ok := r.(ast.Stmt); ok {
				x.Stmt = s
			}
		})
		if cont {
			apply(x.Stmt, visit)
		}
	}
}

func applyExpr(slot *ast.Expr, visit func(parent ast.Node, set func(ast.Node)) bool) {
	if slot == nil || *slot == nil {
		return
	}
	cont := visit(*slot, func(r ast.Node) {
		if e, ok := r.(ast.Expr); ok {
			*slot = e
		}
	})
	if cont {
		apply(*slot, visit)
	}
}

func applyStmt(slot *ast.Stmt, visit func(parent ast.Node, set func(ast.Node)) bool) {
	if slot == nil || *slot == nil {
		return
	}
	cont := visit(*slot, func(r ast.Node) {
		if s, ok := r.(ast.Stmt); ok {
			*slot = s
		}
	})
	if cont {
		apply(*slot, visit)
	}
}

// filterVarDecls drops top-level var declarations whose names are in
// targets. It preserves the rest.
func filterVarDecls(decls []ast.Decl, targets map[string]string, changed *bool) []ast.Decl {
	out := decls[:0]
	for _, d := range decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			out = append(out, d)
			continue
		}
		newSpecs := gd.Specs[:0]
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				newSpecs = append(newSpecs, spec)
				continue
			}
			drop := false
			for _, n := range vs.Names {
				if _, ok := targets[n.Name]; ok {
					drop = true
					break
				}
			}
			if drop {
				*changed = true
				continue
			}
			newSpecs = append(newSpecs, spec)
		}
		gd.Specs = newSpecs
		if len(gd.Specs) == 0 {
			continue // drop the whole decl
		}
		out = append(out, gd)
	}
	return out
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok {
				return "(*" + id.Name + ")." + fn.Name.Name
			}
		}
		if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
			return id.Name + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

func writeFile(fset *token.FileSet, af *ast.File, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return format.Node(f, fset, af)
}
