// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"go/format"
	"go/scanner"
	gotoken "go/token"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The generator: Go from typed translation units.
//
// C's objects are Go allocations of mirror types (the Go type with the
// C type's layout, unions as bytes) and every access goes through a
// *c23.Void. A local whose address is never taken is a Go variable; one
// whose address is, or an array or struct, is a c23.Obj on the Go
// stack. Every local is declared at the top of its function, so that
// goto may jump anywhere Go lets it. A struct value is always in memory:
// as an rvalue it is the *c23.Void to it, a parameter is copied in by the
// callee, and a result is written to memory the caller passes.

// options says what to generate.
type options struct {
	pkg    string // the Go package
	main   bool   // C's main becomes the program's main
	prefix string // of every package-level name: "_C23" in a package that
	// imports "C", so that nothing clashes with the package's own names
	undef bool // a call of an undefined function panics, instead of being an error
	debug bool // write the assertions that Go lays each record out as C does
}

type genError struct{ msg string }

// pkgGen generates the Go of a package: one or more units.
type pkgGen struct {
	o        options
	libcPkgs map[string]bool // the packages of internal/c23/... it calls (libcpkg.go)
	file     string
	units    []*unit
	types    strings.Builder   // the mirror types
	vars     strings.Builder   // package-level variables
	inits    strings.Builder   // func init's statements
	tdescs   map[string]string // the variable holding each mirror type's c23.Type
	tdVars   strings.Builder   // their declarations
	descInit strings.Builder   // the function descriptors' assignments
	funcs    strings.Builder
	mirrors  map[string]string // a record's layout key to its mirror's name
	strs     map[string]string
	ext      map[string]*symbol // the definitions with external linkage
	names    map[*symbol]string
	descs    map[string]bool    // function descriptors made
	objs     map[*symbol]string // the Go expression of an object's value, known statically
	recN     int
	uses     map[string]bool  // imports used
	stubs    map[*symbol]bool // undefined functions that have a stub (options.undef)
	// The 1:1 translation's objects whose initializer is in their Go
	// declaration (thin.go's staticInit), and, while one's is being made,
	// what it refers to.
	declared map[*symbol]bool
	refs     map[*symbol]bool // nil: not being made
	refFunc  bool             // it refers to a function

	// The struct and union types that a unit completes, by what names
	// them in every unit (completeKey): a pointer to one is a pointer
	// to its mirror in a unit that only declares it too. nil for a
	// name that two units give different types.
	complete map[string]*ctype
	fileTmpN int // temporaries of file-scope code, which is all in one function
}

// gen generates one function, or file-scope code, of a unit.
type gen struct {
	*pkgGen
	u     *unit
	unitN int
	out   *strings.Builder
	ind   int

	fn      *funcDecl
	locals  strings.Builder // the function's hoisted declarations
	cells   strings.Builder
	tmpN    int
	inExpr  int    // inside a statement expression's closure
	retType string // the Go result type of the closure being generated
	sret    bool
	flatSt  *flatState // a flat function's (flat.go)
	jmp     bool       // the function calls setjmp: its body is a closure
	frames  []*frame   // the blocks, loops and switches being generated (cleanup.go)

	// The 1:1 translation's locals (thin.go): the ones not declared yet,
	// which are declared where C declares them if nothing can jump
	// over that (inPlace), and at the function's top otherwise; and
	// the names of those declared in place (inBody).
	pending    []*symbol
	inPlace    bool
	inBody     map[string]bool
	declSyms   []*symbol       // of the declaration that is being generated
	localNames map[string]bool // the Go names of the function\'s locals (localName)
}

func (g *gen) errorf(at pos, format string, args ...any) {
	panic(genError{fmt.Sprintf("%s:%d: %s", at.file, at.line, fmt.Sprintf(format, args...))})
}

func (g *gen) p(format string, args ...any) {
	g.out.WriteString(strings.Repeat("\t", g.ind))
	fmt.Fprintf(g.out, format, args...)
	g.out.WriteString("\n")
}

func catch(err *error) {
	if r := recover(); r != nil {
		ge, ok := r.(genError)
		if !ok {
			panic(r)
		}
		*err = fmt.Errorf("%s", ge.msg)
	}
}

func newPkgGen(file string, o options) *pkgGen {
	return &pkgGen{o: o, file: file, mirrors: map[string]string{}, strs: map[string]string{},
		ext: map[string]*symbol{}, names: map[*symbol]string{}, descs: map[string]bool{}, uses: map[string]bool{}, objs: map[*symbol]string{}, tdescs: map[string]string{}}
}

// generate translates units, one program or package, to Go source.
func generate(file string, units []*unit, o options) (src []byte, pg *pkgGen, err error) {
	defer catch(&err)
	if os.Getenv("CCDEBUG") == "layout" {
		o.debug = true
	}
	pg = newPkgGen(file, o)
	pg.units = units
	pg.link()
	pg.completed()
	for i, u := range units {
		g := &gen{pkgGen: pg, u: u, unitN: i}
		g.unit()
	}
	var b strings.Builder
	b.WriteString(pg.types.String())
	b.WriteString(pg.tdVars.String())
	b.WriteString(pg.vars.String())
	if pg.descInit.Len() > 0 {
		b.WriteString("\nfunc init() {\n")
		b.WriteString(pg.descInit.String())
		b.WriteString("}\n")
	}
	if pg.inits.Len() > 0 {
		b.WriteString("\nfunc init() {\n")
		b.WriteString(pg.inits.String())
		b.WriteString("}\n")
	}
	b.WriteString("\n")
	b.WriteString(pg.funcs.String())
	if o.main {
		m := pg.ext["main"]
		if m == nil || m.kind != symFunc || !m.defined {
			return nil, nil, fmt.Errorf("%s: no main function", file)
		}
		args := ""
		if len(m.t.fn.params) >= 2 {
			args = "int32(len(os.Args)), c23.Args()"
			{
				g := &gen{pkgGen: pg, u: units[0]}
				args = "int32(len(os.Args)), " + g.retype("c23.Args()", voidPtr, g.gt(m.t.fn.params[1]))
			}
		}
		ret := fmt.Sprintf("%s(%s)", pg.names[m], args)
		// C's streams are flushed if main panics (a fault): what it
		// printed before is not lost. c23.Exit flushes them, and ends in
		// os.Exit, which runs no deferred call: this one runs only then.
		const flush = "\n\tdefer c23.Fflush(nil) // if main panics"
		if m.t.fn.ret.kind == kVoid {
			fmt.Fprintf(&b, "\nfunc main() {%s\n\t%s\n\tc23.Exit(0)\n}\n", flush, ret)
		} else {
			fmt.Fprintf(&b, "\nfunc main() {%s\n\tc23.Exit(int32(%s))\n}\n", flush, ret)
		}
	}
	// The header, with the imports of what the Go above uses.
	body := b.String()
	var hdr strings.Builder
	fmt.Fprintf(&hdr, "// Code generated by cmd/cc from %s. DO NOT EDIT.\n\npackage %s\n\n", file, o.pkg)
	used := usedPackages(body)
	hdr.WriteString("import (\n")
	for _, std := range []string{"math", "os", "unsafe"} {
		if used[std] {
			fmt.Fprintf(&hdr, "\t%q\n", std)
		}
	}
	hdr.WriteString("\n")
	if used["c23"] {
		hdr.WriteString("\t\"internal/c23\"\n")
	}
	var paths []string
	for path := range pg.libcPkgs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if used[libcAlias(path)] {
			fmt.Fprintf(&hdr, "\t%s %q\n", libcAlias(path), "internal/c23/"+path)
		}
	}
	hdr.WriteString(")\n\n")
	out, ferr := format.Source([]byte(hdr.String() + body))
	if ferr != nil {
		return []byte(hdr.String() + body), pg, fmt.Errorf("formatting the output: %v", ferr)
	}
	return out, pg, nil
}

// usedPackages are the names that src, Go source without its package
// clause, uses as packages: each identifier that a selector follows and
// that nothing selects.
func usedPackages(src string) map[string]bool {
	used := map[string]bool{}
	fset := gotoken.NewFileSet()
	var sc scanner.Scanner
	sc.Init(fset.AddFile("", fset.Base(), len(src)), []byte(src), nil, 0)
	prev, name := gotoken.ILLEGAL, ""
	for {
		_, tok, lit := sc.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok == gotoken.PERIOD && name != "" {
			used[name] = true
		}
		name = ""
		if tok == gotoken.IDENT && prev != gotoken.PERIOD {
			name = lit
		}
		prev = tok
	}
	return used
}

// link names every symbol with linkage, across the units: one definition
// for each external name.
func (pg *pkgGen) link() {
	for i, u := range pg.units {
		for _, s := range u.syms {
			if s.linkage == 'e' {
				if old := pg.ext[s.name]; old != nil {
					if old.defined && s.defined && old.comdat && s.comdat {
						continue // C++'s inline: the first definition
					}
					if old.defined && s.defined && !(s.kind == symVar && (old.init == nil || s.init == nil)) {
						panic(genError{fmt.Sprintf("%s:%d: multiple definition of %q", s.p.file, s.p.line, s.name)})
					}
					if s.defined && (!old.defined || old.init == nil && s.init != nil) {
						pg.ext[s.name] = s
					}
					continue
				}
				pg.ext[s.name] = s
			}
		}
		_ = i
	}
	for i, u := range pg.units {
		for _, s := range u.syms {
			pg.nameGlobal(s, i)
		}
		for _, s := range u.statics {
			pg.nameGlobal(s, i)
		}
	}
}

// goIdent makes a C name a Go identifier.
func goIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '_' || r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case r >= 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "_x%02x_", r)
		}
	}
	return b.String()
}

func (pg *pkgGen) nameGlobal(s *symbol, unitN int) {
	if _, ok := pg.names[s]; ok {
		return
	}
	pre := pg.o.prefix
	kind := "g"
	if s.kind == symFunc {
		kind = "c"
	}
	switch {
	case s.linkage == 'e':
		def := pg.ext[s.name]
		if def != s {
			if n, ok := pg.names[def]; ok {
				pg.names[s] = n
				return
			}
			pg.names[def] = pre + kind + "_" + goIdent(s.name)
		}
		pg.names[s] = pre + kind + "_" + goIdent(s.name)
	case s.linkage == 'i':
		pg.names[s] = fmt.Sprintf("%s%s%d_%s", pre, kind, unitN, goIdent(s.name))
	default:
		pg.recN++
		pg.names[s] = fmt.Sprintf("%s%s%d_%d_%s", pre, kind, unitN, pg.recN, goIdent(strings.ReplaceAll(s.name, ".", "_")))
	}
}

func (g *gen) unit() {
	// File-scope objects and statics: allocated, then initialized in
	// order in init.
	var objs []*symbol
	for _, s := range g.u.syms {
		if s.kind != symVar {
			continue
		}
		if s.linkage == 'e' && g.ext[s.name] != s {
			continue // defined, or tentatively, elsewhere
		}
		objs = append(objs, s)
	}
	// Compound literals at file scope first: initializers use them.
	var lits, rest []*symbol
	for _, s := range g.u.statics {
		if strings.HasPrefix(s.name, "complit.") || strings.HasPrefix(s.name, "pmf.") {
			lits = append(lits, s)
		} else {
			rest = append(rest, s)
		}
	}
	objs = append(append(lits, objs...), rest...)
	for _, s := range g.u.fenv {
		if s.access {
			// The library raises its exceptions from the start.
			g.inits.WriteString("\tc23.UseFenv()\n")
			break
		}
	}
	for _, s := range objs {
		g.globalVar(s)
	}
	for _, f := range g.u.funcs {
		if f.sym.comdat && f.sym.linkage == 'e' && g.ext[f.sym.name] != f.sym {
			continue // C++'s inline: defined by another unit
		}
		g.function(f)
	}
}

func (g *gen) globalVar(s *symbol) {
	g.tglobalVar(s)
	return

}

// Types.

// goType is the Go type of a C value in a Go variable: for C's
// arithmetic types, by the name internal/c23 gives it (c23.Int,
// c23.ULong), which is an alias of the Go type that the data model
// makes it. So the Go says what the C said, and is the same Go whatever
// the size of long.
func goType(t *ctype) string {
	switch t.chr {
	case 'w':
		return "c23.Wchar"
	case 's':
		return "c23.Char16"
	case 'i':
		return "c23.Char32"
	}
	switch t.kind {
	case kBool:
		return "c23.Bool"
	case kUChar:
		return "c23.UChar"
	case kChar:
		return "c23.Char"
	case kSChar:
		return "c23.SChar"
	case kShort:
		return "c23.Short"
	case kUShort:
		return "c23.UShort"
	case kInt:
		return "c23.Int"
	case kUInt:
		return "c23.UInt"
	case kLong:
		return "c23.Long"
	case kULong:
		return "c23.ULong"
	case kLLong:
		return "c23.LongLong"
	case kULLong:
		return "c23.ULongLong"
	case kComplex:
		if t.elem.kind == kFloat {
			return "c23.ComplexFloat"
		}
		return "c23.ComplexDouble"
	case kInt128:
		return "c23.Int128"
	case kUInt128:
		return "c23.Uint128"
	case kBitInt, kUBitInt:
		u := ""
		if t.kind == kUBitInt {
			u = "u"
		}
		return fmt.Sprintf("%sint%d", u, t.sizeof()*8)
	case kFloat:
		return "c23.Float"
	case kDouble, kLDouble:
		return "c23.Double" // long double's value is a double
	case kPtr, kNullptr, kVaList, kStruct, kArray, kIntCap, kUIntCap:
		return "*c23.Void"
	}
	panic(fmt.Sprintf("no Go type for %s", t))
}

// mirror is the Go type with t's layout, with pointers where t has
// them: what C's memory is, and what Go sees it as.
func (g *gen) mirror(t *ctype) string {
	switch t.kind {
	case kIntCap, kUIntCap:
		return "c23.Intcap"
	case kPtr:
		e := t.elem
		switch {
		case e.kind == kFunc:
			// To a function's descriptor (c23.Func), or an integer C
			// keeps in its place (SIG_IGN).
			return voidPtr
		case e.kind == kStruct && !e.rec.complete:
			// Declared only, here: another unit may complete it.
			if c := g.complete[completeKey(e)]; c != nil {
				return "*" + g.mirror(c)
			}
			return voidPtr
		case e.kind == kVoid || e.kind == kArray && e.n < 0:
			return voidPtr
		}
		return "*" + g.mirror(e)
	case kArray:
		return fmt.Sprintf("[%d]%s", max(t.n, 0), g.mirror(t.elem))
	case kStruct:
		if !t.rec.complete {
			return "byte"
		}
		return g.record(t)
	case kVoid:
		return "byte"
	case kNullptr:
		return voidPtr
	case kVaList:
		return voidPtr
	case kLDouble:
		return "c23.LDouble"
	case kComplex:
		return fmt.Sprintf("[2]%s", g.mirror(t.elem))
	case kFunc:
		return "c23.Func"
	}
	return goType(t)
}

// record returns the name of a struct or union's mirror, made the first
// time: one per layout, so that every unit shares it.
func (g *gen) record(t *ctype) string {
	r := t.rec
	if r.goName != "" {
		return r.goName
	}
	bytes := r.union || r.packed > 0 || t.rec.align > 8 || !naturalLayout(r)
	defer func() { r.bytes = bytes }()
	var fields []string
	for i, f := range r.fields {
		if f.bits >= 0 || f.packed || f.t.align > f.t.unqual().alignofNatural() {
			bytes = true
		}
		if i == len(r.fields)-1 && f.t.sizeof() == 0 {
			// Go pads a struct that ends in a zero-size field.
			bytes = true
		}
	}
	// The key: the layout, which identifies the mirror. A C++ class's
	// tag does not say which class it is (a template's instances share
	// it, and classes of several scopes may), nor do its fields' types'
	// tags: there the names are the mangled ones.
	var key strings.Builder
	keyTag, typeKey := r.tag, (*ctype).String
	if r.sc != nil {
		keyTag, typeKey = rawType(t.unqual()), cxxTypeKey
		if unnamedMember(t) {
			// It has no name of its own, and the number that stands
			// for one is its translation unit's: the scope it is
			// declared in, and below its members, say which it is.
			keyTag = strings.Join(scopeNames(r.sc), "::") + "::"
		}
	} else if keyTag == "" {
		keyTag = r.tdName // div_t and ldiv_t are not one mirror
	}
	fmt.Fprintf(&key, "%s|%d|%d|%v|", keyTag, r.size, r.align, bytes)
	if !bytes || unnamedMember(t) {
		for _, f := range r.fields {
			fmt.Fprintf(&key, "%s:%d:%s;", f.name, f.off, typeKey(f.t))
		}
	}
	if n, ok := g.mirrors[key.String()]; ok {
		r.goName = n
		return n
	}
	g.recN++
	kw := "struct"
	if r.union {
		kw = "union"
	}
	tag, what := r.tag, kw+" "+r.tag
	if tag == "" && r.tdName != "" {
		tag, what = r.tdName, r.tdName
	}
	if tag == "" {
		tag, what = "anon", "unnamed "+kw
	}
	name := fmt.Sprintf("%s%s_%s", g.o.prefix, kw, goIdent(tag))
	if g.mirrorTaken(name) {
		name = fmt.Sprintf("%s%s_%s_%d", g.o.prefix, kw, goIdent(tag), g.recN)
	}
	r.goName = name
	g.mirrors[key.String()] = name
	if !bytes {
		for _, f := range r.fields {
			fields = append(fields, fmt.Sprintf("%s %s", g.fieldName(f, len(fields)), g.mirror(f.t)))
		}
	}
	var b strings.Builder
	if bytes {
		// As cgo does for unions, Go sees the bytes. The tag tells
		// internal/c23 that they may hold pointers C stores, whose
		// capabilities it keeps aside.
		if w := target.ptrSize; hasPointers(t) && r.size > 0 && r.size%w == 0 && r.align%w == 0 {
			// A member may be a pointer: Go sees words, each of which
			// the collector takes for one.
			fmt.Fprintf(&b, "// %s is C's %s, as Go sees it: its words, which may hold pointers.\ntype %s struct {\n%s\tb [%d]unsafe.Pointer\n}\n\n",
				name, what, name, unionAlign(r.align), r.size/w)
		} else {
			fmt.Fprintf(&b, "// %s is C's %s, as Go sees it: its bytes.\ntype %s struct {\n%s\tb [%d]byte `c23:\"union\"`\n}\n\n",
				name, what, name, unionAlign(r.align), r.size)
		}
	} else {
		fmt.Fprintf(&b, "// %s is C's %s, as Go sees it.\ntype %s struct {\n", name, what, name)
		for _, f := range fields {
			fmt.Fprintf(&b, "\t%s\n", f)
		}
		b.WriteString("}\n\n")
		if g.o.debug {
			for i, f := range r.fields {
				fn := g.fieldName(f, i)
				fmt.Fprintf(&b, "var _ [unsafe.Offsetof(%s{}.%s) - %d]byte\nvar _ [%d - unsafe.Offsetof(%s{}.%s)]byte\n",
					name, fn, f.off, f.off, name, fn)
			}
		}
	}
	if g.o.debug {
		// Go lays the mirror out as C lays the record out, or the
		// package does not compile (cc -debug).
		fmt.Fprintf(&b, "var _ [unsafe.Sizeof(%s{}) - %d]byte\nvar _ [%d - unsafe.Sizeof(%s{})]byte\n\n", name, r.size, r.size, name)
	}
	g.types.WriteString(b.String())
	return name
}

// completeKey is what names the struct or union type t in every unit of
// the program: a C++ class's mangled name, a C tag of file scope. "" if
// nothing does.
func completeKey(t *ctype) string {
	r := t.rec
	if r.tag == "" {
		return ""
	}
	for sc := r.sc; sc != nil; sc = sc.parent {
		if sc.kind == skBlock {
			return "" // a block's: its function's only
		}
	}
	if r.sc != nil {
		return "c++:" + rawType(t.unqual())
	}
	return "c:" + r.tag
}

// completed finds the types that each unit completes.
func (pg *pkgGen) completed() {
	pg.complete = map[string]*ctype{}
	layout := map[string]string{}
	for _, u := range pg.units {
		for _, t := range u.records {
			k := completeKey(t)
			if k == "" || !t.rec.complete {
				continue
			}
			if t.rec.sc == nil && u.tags[t.rec.tag] != t && (u.tags[t.rec.tag] == nil || u.tags[t.rec.tag].rec != t.rec) {
				continue // a tag of a block's scope
			}
			// Two units' types of one name are one type if they are
			// laid out alike: else the name says nothing.
			var l strings.Builder
			fmt.Fprintf(&l, "%d|%d|", t.rec.size, t.rec.align)
			for _, f := range t.rec.fields {
				fmt.Fprintf(&l, "%s:%d:%d;", f.name, f.off, f.t.sizeof())
			}
			if old, ok := layout[k]; ok && old != l.String() {
				pg.complete[k] = nil
				continue
			} else if ok {
				continue
			}
			layout[k] = l.String()
			pg.complete[k] = t.unqual()
		}
	}
}

// unnamedMember reports whether t is a struct or union with no name that
// a class or a namespace declares: one that every unit that includes
// its declaration has, as the same type. (One that a block declares is
// its function's only.)
func unnamedMember(t *ctype) bool {
	if t.kind != kStruct || t.rec.tag != "" || t.rec.sc == nil {
		return false
	}
	for sc := t.rec.sc; sc != nil; sc = sc.parent {
		if sc.kind == skBlock {
			return false
		}
	}
	return true
}

// cxxTypeKey is what says which type t is in the key of a C++ class's
// mirror: its mangled name, but for a class with no name, which is its
// members.
func cxxTypeKey(t *ctype) string {
	switch {
	case unnamedMember(t):
		var b strings.Builder
		fmt.Fprintf(&b, "{%d|%d|%v|", t.rec.size, t.rec.align, t.rec.union)
		for _, f := range t.rec.fields {
			fmt.Fprintf(&b, "%s:%d:%s;", f.name, f.off, cxxTypeKey(f.t))
		}
		return b.String() + "}"
	case t.kind == kArray:
		return fmt.Sprintf("A%d_%s", t.n, cxxTypeKey(t.elem))
	case t.kind == kPtr && unnamedMember(t.elem):
		return "P" + cxxTypeKey(t.elem)
	}
	return rawType(t)
}

func (g *gen) mirrorTaken(name string) bool {
	for _, n := range g.mirrors {
		if n == name {
			return true
		}
	}
	return false
}

// fieldName is a member's Go name: cgo's (a Go keyword with _ before it).
func (g *gen) fieldName(f *field, i int) string {
	if f.name == "" {
		return fmt.Sprintf("_anon%d", i)
	}
	return goField(f.name)
}

func (t *ctype) alignofNatural() int64 {
	c := *t
	c.align = 0
	return c.alignof()
}

var goKeywords = map[string]bool{"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true, "map": true, "package": true, "range": true,
	"return": true, "select": true, "struct": true, "switch": true, "type": true, "var": true}

// goField is the Go name of a C member: cgo's, a Go keyword prefixed with _.
func goField(name string) string {
	if goKeywords[name] {
		return "_" + name
	}
	return goIdent(name)
}

// unionAlign is the zero-size field that gives a union's bytes its
// alignment.
func unionAlign(a int64) string {
	switch {
	case a >= 8:
		return "\t_ [0]uint64\n"
	case a == 4:
		return "\t_ [0]uint32\n"
	case a == 2:
		return "\t_ [0]uint16\n"
	}
	return ""
}

// Constants.

func constLit(t *ctype, v int64) string {
	switch {
	case t.kind == kInt128:
		return fmt.Sprintf("c23.I128(%d)", v)
	case t.kind == kUInt128:
		return fmt.Sprintf("c23.U128(%d)", v)
	case t.kind == kUBitInt:
		return fmt.Sprintf("%s(%d)", goType(t), uint64(wrap(t, v)))
	case t.isPtr() || t.kind == kNullptr || t.kind == kVaList || t.isCap():
		if v == 0 {
			return "(*c23.Void)(nil)"
		}
		return fmt.Sprintf("c23.FromInt(%d)", uint64(v)&(1<<(8*target.ptrSize)-1))
	case t.isFloat():
		return fmt.Sprintf("%s(%d)", goType(t), v)
	case t.isUnsigned() && t.sizeof() == 8 && !t.isBitInt():
		return fmt.Sprintf("%s(%d)", goType(t), uint64(v))
	case t.isUnsigned() && t.sizeof() == 8:
		return fmt.Sprintf("uint64(%d)", uint64(v))
	}
	return fmt.Sprintf("%s(%d)", goType(t), wrap(t, v))
}

func floatLit(t *ctype, v float64) string {
	switch {
	case v != v:
		return fmt.Sprintf("%s(math.NaN())", goType(t))
	case math.IsInf(v, 1):
		return fmt.Sprintf("%s(math.Inf(1))", goType(t))
	case math.IsInf(v, -1):
		return fmt.Sprintf("%s(math.Inf(-1))", goType(t))
	case v == 0 && 1/v < 0:
		return fmt.Sprintf("%s(math.Copysign(0, -1))", goType(t))
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if t.kind == kFloat {
		s = strconv.FormatFloat(float64(float32(v)), 'g', -1, 32)
	}
	return fmt.Sprintf("%s(%s)", goType(t), s)
}

// str returns the Go variable of a string literal's array.
func (g *gen) str(s *strLit) string {
	key := s.t.elem.String() + "|" + s.data
	if n, ok := g.strs[key]; ok {
		return n
	}
	n := fmt.Sprintf("%ss_%d", g.o.prefix, len(g.strs))
	g.strs[key] = n
	esz := s.t.elem.sizeof()
	data := s.data + strings.Repeat("\x00", int(esz))
	el := g.mirror(s.t.elem)
	if esz == 1 {
		// The Go constant itself: a 0 byte follows it.
		delete(g.strs, key)
		return fmt.Sprintf("(*c23.Void)(unsafe.Pointer(c23.String(%s)))", strconv.Quote(s.data))
	}
	fmt.Fprintf(&g.vars, "var %s = c23.StringOf[%s, [%d]%s](%s)\n", n, el, int64(len(data))/esz, el, strconv.Quote(data))
	return n
}

// sorted returns m's keys in order.
func sorted[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// naturalLayout reports whether r has the layout Go gives a struct of
// its members: no attribute changed it.
func naturalLayout(r *record) bool {
	off, align := int64(0), int64(1)
	for _, f := range r.fields {
		if f.bits >= 0 {
			return false
		}
		a := f.t.unqual().alignofNatural()
		if f.t.kind == kStruct {
			a = min(f.t.rec.align, 8)
		}
		off = (off + a - 1) / a * a
		if off != f.off {
			return false
		}
		off += f.t.sizeof()
		align = max(align, a)
	}
	size := (off + align - 1) / align * align
	return size == r.size && align == r.align
}

// constLitStr is constLit of a constant written in decimal.
func constLitStr(t *ctype, s string) string {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		u, _ := strconv.ParseUint(s, 10, 64)
		v = int64(u)
		if t.kind == kInt128 {
			return fmt.Sprintf("c23.I128U(%d)", u)
		}
		return fmt.Sprintf("c23.U128U(%d)", u)
	}
	return constLit(t, v)
}

// typeDesc returns the package variable that holds the c23.Type of the
// mirror type m, which the allocations in functions pass, so that they
// do not look the type up each time.
func (g *pkgGen) typeDesc(m string) string {
	if n, ok := g.tdescs[m]; ok {
		return n
	}
	n := fmt.Sprintf("tdesc%d", len(g.tdescs))
	g.tdescs[m] = n
	fmt.Fprintf(&g.tdVars, "var %s = c23.TypeOf[%s]()\n", n, m)
	return n
}
