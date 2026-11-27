// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Units (cc -units dir; doc/gd/cc.md).
//
// Each C file is translated to a Go file of its own, which cmd/compile
// compiles by itself (a file that begins with //go:unit), as a C
// compiler compiles the C file: so that a change to one C file
// translates and compiles that one, and the rest is taken from where it
// was kept. The Go of a unit is made of that unit alone:
//
//   - It declares every type it uses. A type has the same name in every
//     unit, made of what names it in C (a class's mangled name) or of
//     its layout, and the compiler makes the units' types of one name
//     one type. A class that the unit only has declared is a type with
//     no size, declared so (c23.Incomplete): the unit that defines the
//     class gives the type.
//   - A function with external linkage that it defines has its C name,
//     and one that it only calls is declared without a body. An inline
//     function, or a template's, is defined by every unit that uses it
//     and marked //go:dupok: each unit inlines its own, and the linker
//     keeps one.
//   - An object with external linkage is declared by every unit that
//     names it, and is one object: the unit that defines it gives it
//     its value. An object that several units define (an inline
//     variable, a template's static member, a table of virtual
//     functions, a static local of an inline function) is given its
//     constants by each, and is initialized by code once.
//   - What has internal linkage has the unit's tag in its name.
//   - Nothing is initialized by Go: a unit has two functions, one that
//     makes its objects and gives them their constants (cc_initA_tag)
//     and one that runs its initializers (cc_initB_tag). The root, the
//     one file of the package that is compiled as Go is, calls every
//     unit's first function, then every unit's second, then main.
//
// A unit's Go is kept with the list of what its translation read: it
// is translated again only if one of those files changed.

const unitMarker = "//go:unit\n"

// unitInfo is what a unit's translation tells the root, kept beside
// its Go (tag.ccu).
type unitInfo struct {
	Tag     string
	File    string
	Options string            // the translation's options: another's Go is not this one's
	Imports []string          // the packages its Go imports
	Defs    []string          // the functions with external linkage that it defines
	Undefs  map[string]string // those it calls and does not define: the Go name, and C's
	Opaques []string          // the types it has only declared
	Types   map[string]string // the classes it defines: the Go type, and its layout
	Main    bool              // it has main
	Inputs  map[string]string // what the translation read, and its hash ("" for a file that was not there)
}

func (in *unitInfo) def(name string, dupok bool) {
	in.Defs = append(in.Defs, name)
}

func (in *unitInfo) undef(goName, cName string) {
	if in.Undefs == nil {
		in.Undefs = map[string]string{}
	}
	in.Undefs[goName] = cName
}

func (in *unitInfo) setImports(used map[string]bool, libc map[string]bool) {
	if used["c23"] {
		in.Imports = append(in.Imports, "internal/c23")
	}
	for path := range libc {
		if used[libcAlias(path)] {
			in.Imports = append(in.Imports, "internal/c23/"+path)
		}
	}
	sort.Strings(in.Imports)
}

// unitPre is what a unit's own names begin with after their kind: its
// tag. "" if the Go is not a unit's.
func (pg *pkgGen) unitPre() string {
	if pg.o.unit == "" {
		return ""
	}
	return pg.o.unit + "_"
}

func hash8(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}

// sharedStatic reports whether s is a static local of a function with
// external linkage (or its guard): the object of every unit that has
// the function, which an inline function's are.
func sharedStatic(s *symbol) bool {
	return s.staticIn != nil && s.staticIn.kind == symFunc && s.staticIn.linkage == 'e' && !strings.Contains(s.staticIn.name, "._")
}

// staticName is the name of such an object: its function's, and which
// of the function's statics it is.
func staticName(s *symbol) string {
	fn := goIdent(s.staticIn.name)
	if s.staticN < 0 {
		return fmt.Sprintf("%s__guard_%d", fn, -1-s.staticN)
	}
	name, _, _ := strings.Cut(s.name, ".")
	return fmt.Sprintf("%s__%s_%d", fn, goIdent(name), s.staticN)
}

// sharedVar reports whether several units may define the object s.
func sharedVar(s *symbol) bool {
	return s.kind == symVar && (s.comdat && s.linkage == 'e' || sharedStatic(s))
}

// unitOwn reports whether a C++ class named by key (completeKey) is the
// unit's own, whatever another unit calls the same: a class of an
// unnamed namespace, or one with no name.
func unitOwn(t *ctype, key string) bool {
	if strings.Contains(key, "._") || strings.Contains(key, "_GLOBAL__N_1") {
		return true
	}
	return false
}

// unitTypeName is the Go name of a class in every unit, from what
// names it in all of them.
func (pg *pkgGen) unitTypeName(t *ctype, kw, tag, key string) string {
	if unitOwn(t, key) {
		key += "|" + pg.o.unit
	}
	return fmt.Sprintf("%s%s_%s_%s", pg.o.prefix, kw, goIdent(tag), hash8(key))
}

// unitRecordName is the Go name of the complete struct or union t in a
// unit: layout is its layout's key (record).
func (g *gen) unitRecordName(t *ctype, kw, tag, layout string) string {
	if k := completeKey(t); strings.HasPrefix(k, "c++:") {
		name := g.unitTypeName(t, kw, tag, k)
		if g.info.Types == nil {
			g.info.Types = map[string]string{}
		}
		g.info.Types[name] = hash8(layout)
		return name
	}
	return fmt.Sprintf("%s%s_%s_%s", g.o.prefix, kw, goIdent(tag), hash8(layout))
}

// opaque is the Go type of the class t, which the unit has only
// declared: "" if the Go is not a unit's, or nothing names the type in
// every unit (C's, whose tags are each file's).
func (g *gen) opaque(t *ctype) string {
	if g.o.unit == "" {
		return ""
	}
	k := completeKey(t)
	if !strings.HasPrefix(k, "c++:") {
		return ""
	}
	kw := "struct"
	if t.rec.union {
		kw = "union"
	}
	name := g.unitTypeName(t, kw, t.rec.tag, k)
	for _, o := range g.info.Opaques {
		if o == name {
			return name
		}
	}
	g.info.Opaques = append(g.info.Opaques, name)
	fmt.Fprintf(&g.types, "// %s is C's %s %s, which this file has only declared.\ntype %s struct{ _ c23.Incomplete }\n\n", name, kw, t.rec.tag, name)
	return name
}

// unitInits writes the unit's two functions.
func (pg *pkgGen) unitInits(b *strings.Builder) {
	writeInits(b, "cc_initA_"+pg.o.unit, pg.prelude.String()+pg.descInit.String()+pg.inits.String())
	writeInits(b, "cc_initB_"+pg.o.unit, pg.dynInits.String())
}

// unitMain writes cc_main, which calls C's main, in the unit that has
// it.
func (pg *pkgGen) unitMain(b *strings.Builder, u *unit) {
	m := pg.ext["main"]
	if !pg.o.main || m == nil || m.kind != symFunc || !m.defined {
		return
	}
	pg.info.Main = true
	b.WriteString(pg.mainFunc("cc_main", m, u))
}

// mainFunc is the Go function name that calls C's main, m, and exits
// with what it returns.
func (pg *pkgGen) mainFunc(name string, m *symbol, u *unit) string {
	args := ""
	if len(m.t.fn.params) >= 2 {
		g := &gen{pkgGen: pg, u: u}
		args = "int32(len(os.Args)), " + g.retype("c23.Args()", voidPtr, g.gt(m.t.fn.params[1]))
	}
	ret := fmt.Sprintf("%s(%s)", pg.names[m], args)
	// C's streams are flushed if main panics (a fault): what it
	// printed before is not lost. c23.Exit flushes them, and ends in
	// os.Exit, which runs no deferred call: this one runs only then.
	const flush = "\n\tdefer c23.Fflush(nil) // if main panics"
	if m.t.fn.ret.kind == kVoid {
		return fmt.Sprintf("\nfunc %s() {%s\n\t%s\n\tc23.Exit(0)\n}\n", name, flush, ret)
	}
	return fmt.Sprintf("\nfunc %s() {%s\n\tc23.Exit(int32(%s))\n}\n", name, flush, ret)
}

// unitTag is the tag of the unit that is the translation of file: what
// its Go file is called, and what its own names have in them.
func unitTag(file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	base := filepath.Base(file)
	return goIdent(strings.ReplaceAll(base, ".", "_")) + "_" + hash8(abs)[:6]
}

// fileHashes are the hashes of the files that units' translations
// read, each computed once.
type fileHashes struct {
	mu sync.Mutex
	m  map[string]string
}

func (h *fileHashes) of(path string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.m[path]; ok {
		return s
	}
	s := ""
	if b, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(b)
		s = hex.EncodeToString(sum[:])
	}
	h.m[path] = s
	return s
}

// ccKey says which cc this is: another's translation is not kept.
func ccKey() string {
	exe, err := os.Executable()
	if err != nil {
		return "?"
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return "?"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// translateUnits translates each of files to a unit in dir, or keeps
// the unit that is there if nothing its translation read has changed,
// and writes the root. It reports how many it translated.
func translateUnits(files []string, o options, incDirs []string, dir string) (translated int, err error) {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return 0, err
	}
	opts := fmt.Sprintf("%s|%s|%v|%q|%s", o.pkg, o.prefix, o.debug, incDirs, ccKey())
	infos := make([]*unitInfo, len(files))
	errs := make([]error, len(files))
	hashes := &fileHashes{m: map[string]string{}}
	var count atomic.Int64
	one := func(i int) {
		f := files[i]
		tag := unitTag(f)
		goFile, infoFile := filepath.Join(dir, tag+".go"), filepath.Join(dir, tag+".ccu")
		if old := readUnitInfo(infoFile); old != nil && old.Options == opts && old.File == f {
			fresh := true
			for path, sum := range old.Inputs {
				if hashes.of(path) != sum {
					fresh = false
					break
				}
			}
			if _, err := os.Stat(goFile); err != nil {
				fresh = false
			}
			if fresh {
				infos[i] = old
				return
			}
		}
		in := newInputs()
		b, err := in.readFile(f)
		if err != nil {
			errs[i] = err
			return
		}
		toks, err := lexUnitIn([]source{{f, string(b)}}, incDirs, in)
		if err != nil {
			errs[i] = err
			return
		}
		u, err := parse(f, toks)
		if err != nil {
			errs[i] = err
			return
		}
		uo := o
		uo.unit = tag
		src, pg, err := generate(f, []*unit{u}, uo)
		if err != nil {
			if src != nil {
				os.WriteFile(goFile, src, 0o666) // to look at
			}
			errs[i] = err
			return
		}
		// (A copy: the unit's trees, which the generator has, are
		// not kept with it.)
		info := new(unitInfo)
		*info = pg.info
		info.Tag, info.File, info.Options = tag, f, opts
		info.Inputs = map[string]string{}
		for path, sum := range in.files {
			info.Inputs[path] = hex.EncodeToString(sum[:])
		}
		for path := range in.missing {
			info.Inputs[path] = ""
		}
		data, _ := json.Marshal(info)
		if err := writeRename(goFile, src); err != nil {
			errs[i] = err
			return
		}
		if err := writeRename(infoFile, data); err != nil {
			errs[i] = err
			return
		}
		infos[i] = info
		count.Add(1)
	}
	n := min(runtime.GOMAXPROCS(0), len(files))
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				one(i)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return int(count.Load()), err
		}
	}
	// The units of files that are no longer the program's.
	have := map[string]bool{}
	for _, in := range infos {
		have[in.Tag] = true
	}
	if old, err := filepath.Glob(filepath.Join(dir, "*.ccu")); err == nil {
		for _, name := range old {
			if tag := strings.TrimSuffix(filepath.Base(name), ".ccu"); !have[tag] {
				os.Remove(name)
				os.Remove(filepath.Join(dir, tag+".go"))
			}
		}
	}
	root, err := unitRoot(infos, o)
	if err != nil {
		return int(count.Load()), err
	}
	rootFile := filepath.Join(dir, "cc_root.go")
	if old, err := os.ReadFile(rootFile); err != nil || string(old) != string(root) {
		if err := writeRename(rootFile, root); err != nil {
			return int(count.Load()), err
		}
	}
	return int(count.Load()), nil
}

func readUnitInfo(name string) *unitInfo {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil
	}
	in := new(unitInfo)
	if json.Unmarshal(data, in) != nil {
		return nil
	}
	return in
}

// unitRoot is the Go of the root of the units: the file of the package
// that is compiled as Go is, which has what no unit can have alone.
func unitRoot(infos []*unitInfo, o options) ([]byte, error) {
	imports := map[string]bool{"internal/c23": true}
	defs := map[string]string{}
	types := map[string]string{}
	typeIn := map[string]string{}
	opaque := map[string]bool{}
	undef := map[string]string{}
	undefIn := map[string]string{}
	main := ""
	for _, in := range infos {
		for _, p := range in.Imports {
			imports[p] = true
		}
		for _, d := range in.Defs {
			defs[d] = in.File
		}
		for name, layout := range in.Types {
			if old, ok := types[name]; ok && old != layout {
				return nil, fmt.Errorf("%s and %s define a class of one name differently (Go type %s): one of them must be in an unnamed namespace", typeIn[name], in.File, name)
			}
			types[name], typeIn[name] = layout, in.File
		}
		if in.Main {
			if main != "" {
				return nil, fmt.Errorf("%s and %s both define main", main, in.File)
			}
			main = in.File
		}
	}
	for _, in := range infos {
		for _, name := range in.Opaques {
			if _, ok := types[name]; !ok {
				opaque[name] = true
			}
		}
		for goName, cName := range in.Undefs {
			if _, ok := defs[goName]; !ok {
				undef[goName], undefIn[goName] = cName, in.File
			}
		}
	}
	if o.main && main == "" {
		return nil, fmt.Errorf("no main function")
	}
	if len(undef) > 0 && !o.undef {
		var list []string
		for _, name := range sorted(undef) {
			list = append(list, fmt.Sprintf("\n\t%s: undefined reference to %q", undefIn[name], undef[name]))
		}
		if len(list) > 20 {
			list = append(list[:20], fmt.Sprintf("\n\t... and %d more", len(list)-20))
		}
		return nil, fmt.Errorf("undefined functions:%s", strings.Join(list, ""))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/cc from the units of this directory, as their root. DO NOT EDIT.\n\npackage %s\n\nimport (\n", o.pkg)
	for _, p := range sorted(imports) {
		if p == "internal/c23" {
			fmt.Fprintf(&b, "\t%q\n", p)
		} else {
			fmt.Fprintf(&b, "\t_ %q\n", p)
		}
	}
	b.WriteString(")\n\n// The units' functions that make their objects, and that initialize them.\n")
	for _, in := range infos {
		fmt.Fprintf(&b, "func cc_initA_%s()\nfunc cc_initB_%s()\n", in.Tag, in.Tag)
	}
	if len(opaque) > 0 {
		b.WriteString("\n// The classes that no unit defines.\n")
		for _, name := range sorted(opaque) {
			fmt.Fprintf(&b, "type %s struct{ _ c23.Incomplete }\n", name)
		}
		b.WriteString("\nvar cc_opaque = [...]any{\n")
		for _, name := range sorted(opaque) {
			fmt.Fprintf(&b, "\t(*%s)(nil),\n", name)
		}
		b.WriteString("}\n")
	}
	if len(undef) > 0 {
		b.WriteString("\n// The functions that no unit defines (cc -undef).\n")
		for _, name := range sorted(undef) {
			fmt.Fprintf(&b, "func %s() { panic(%q) }\n", name, "cc: call of the undefined function "+undef[name])
		}
	}
	b.WriteString("\n// cc_init makes every unit's objects, then runs every unit's initializers.\nfunc cc_init() {\n\t_ = c23.Incomplete{}\n")
	if len(opaque) > 0 {
		b.WriteString("\t_ = cc_opaque\n")
	}
	for _, in := range infos {
		fmt.Fprintf(&b, "\tcc_initA_%s()\n", in.Tag)
	}
	for _, in := range infos {
		fmt.Fprintf(&b, "\tcc_initB_%s()\n", in.Tag)
	}
	b.WriteString("}\n")
	if o.main {
		b.WriteString("\nfunc cc_main()\n\nfunc main() {\n\tcc_init()\n\tcc_main()\n}\n")
	} else {
		b.WriteString("\nfunc init() { cc_init() }\n")
	}
	return []byte(b.String()), nil
}
