// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package test

import (
	"bytes"
	"flag"
	"fmt"
	"internal/testenv"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEscapeBitsRandom checks the safety properties of escape-bits
// (doc/gd/escape-bits.md, Safety properties) on random programs.
//
// A program is a set of functions made of the statements that the
// optimisation looks at: locals whose address goes to dynamic callees
// that keep it, return it, or neither; allocations that reach results,
// each other, the heap, enclosing loops and closures; calls of other
// such functions. Each function runs deep in the stack, which is then
// overwritten, and prints what it and the pointers it left behind
// see.
//
// The program is built twice: as usual, and with -d=gdescbitsoff,
// which moves every such local to the heap and passes no result
// buffers, as stock Go does. Both must print the same (S3, S4: the
// optimisation is not observable). The first also runs with
// GODEBUG=stackptrcheck=1, so that the collector, which each function
// runs, throws if it finds a pointer into a returned frame (S1, S2).

var (
	escBitsRandomSeed  = flag.Int64("escbits.seed", 1, "first seed of TestEscapeBitsRandom")
	escBitsRandomProgs = flag.Int("escbits.progs", 2, "number of programs TestEscapeBitsRandom generates")
	escBitsRandomFuncs = flag.Int("escbits.funcs", 150, "functions in each program of TestEscapeBitsRandom")
	escBitsRandomKeep  = flag.String("escbits.keep", "", "directory to also write the programs of TestEscapeBitsRandom to")
)

func TestEscapeBitsRandom(t *testing.T) {
	testenv.MustHaveGoBuild(t)
	if testing.Short() {
		t.Skip("builds programs")
	}
	gotool := testenv.GoToolPath(t)
	dir := t.TempDir()
	for i := 0; i < *escBitsRandomProgs; i++ {
		seed := *escBitsRandomSeed + int64(i)
		src := ebrGenerate(seed, *escBitsRandomFuncs)
		file := filepath.Join(dir, fmt.Sprintf("prog%d.go", seed))
		if err := os.WriteFile(file, src, 0o666); err != nil {
			t.Fatal(err)
		}
		if d := *escBitsRandomKeep; d != "" {
			os.WriteFile(filepath.Join(d, filepath.Base(file)), src, 0o666)
		}
		fail := func(format string, args ...any) {
			t.Helper()
			t.Errorf("seed %d: %s", seed, fmt.Sprintf(format, args...))
		}
		run := func(name string, env []string, gcflags string) ([]byte, bool) {
			exe := filepath.Join(dir, fmt.Sprintf("prog%d-%s", seed, name))
			args := []string{"build", "-o", exe}
			if gcflags != "" {
				args = append(args, "-gcflags="+gcflags)
			}
			cmd := testenv.Command(t, gotool, append(args, file)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				fail("building (%s): %v\n%s", name, err, ebrTail(out))
				return nil, false
			}
			cmd = testenv.Command(t, exe)
			cmd.Env = append(os.Environ(), env...)
			out, err := cmd.CombinedOutput()
			os.Remove(exe)
			if err != nil {
				fail("running (%s): %v\n%s", name, err, ebrTail(out))
				return nil, false
			}
			return out, true
		}
		want, ok := run("off", nil, "-d=gdescbitsoff")
		if !ok {
			continue
		}
		got, ok := run("on", []string{"GODEBUG=stackptrcheck=1"}, "")
		if !ok {
			continue
		}
		if !bytes.Equal(got, want) {
			fail("output differs from the one without escape-bits:\n%s", ebrDiff(got, want))
		}
	}
}

func ebrTail(out []byte) []byte {
	if len(out) > 3000 {
		out = out[len(out)-3000:]
	}
	return out
}

// ebrDiff returns the first lines where got and want differ.
func ebrDiff(got, want []byte) string {
	g, w := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	var b strings.Builder
	n := 0
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			fmt.Fprintf(&b, "line %d:\n\tgot  %s\n\twant %s\n", i+1, gl, wl)
			if n++; n == 5 {
				break
			}
		}
	}
	return b.String()
}

// ebrPrelude is what every generated program starts with: the node
// type, the dynamic callees (set where the compiler can't see), and
// the harness.
const ebrPrelude = `package main

import (
	"fmt"
	"os"
	"runtime"
)

type N struct {
	v    int
	next *N
	pad  [2]int
}

var sink []*N

// Dynamic callees.
var D struct {
	keep    func(*N)
	peek    func(*N) int
	ret     func(*N) *N
	retAny  func(*N) any
	retNext func(*N) *N
	link    func(a, b *N)
	keep2nd func(a, b *N)
	mk      func(int) *N
	call    func(func())
}

type I interface {
	Keep(*N)
	Peek(*N) int
	Ret(*N) *N
	Mk(int) *N
}

type impl struct{ k int }

func (m impl) Keep(p *N)     { sink = append(sink, p) }
func (m impl) Peek(p *N) int { return p.v + m.k }
func (m impl) Ret(p *N) *N   { return p }
func (m impl) Mk(c int) *N   { return &N{v: c + m.k} }

type quiet struct{ n *int }

func (m quiet) Keep(p *N)     { *m.n += p.v }
func (m quiet) Peek(p *N) int { return p.v }
func (m quiet) Ret(p *N) *N   { return nil }
func (m quiet) Mk(c int) *N   { return nil }

type fwd struct{ I }

type pfwd struct {
	n  int
	in I
}

func (f *pfwd) Keep(p *N)     { f.in.Keep(p) }
func (f *pfwd) Peek(p *N) int { return f.in.Peek(p) }
func (f *pfwd) Ret(p *N) *N   { return f.in.Ret(p) }
func (f *pfwd) Mk(c int) *N   { return f.in.Mk(c) }

var ifaces [5]I
var quietN int

func init() {
	D.keep = func(p *N) { sink = append(sink, p) }
	D.peek = func(p *N) int { return p.v }
	D.ret = func(p *N) *N { return p }
	D.retAny = func(p *N) any { return p }
	D.retNext = func(p *N) *N { return p.next }
	D.link = func(a, b *N) { a.next = b }
	D.keep2nd = func(a, b *N) { a.v++; sink = append(sink, b) }
	D.mk = mkLit
	D.call = func(f func()) { f() }
	ifaces = [5]I{impl{1}, quiet{&quietN}, fwd{impl{2}}, &pfwd{in: impl{3}}, &pfwd{in: fwd{quiet{&quietN}}}}
	if len(os.Args) > 7 {
		D.keep, D.mk = nil, nil
	}
}

// Static callees.

//go:noinline
func mkLit(c int) *N { return &N{v: c} }

//go:noinline
func mkNew(c int) *N {
	p := new(N)
	p.v = c
	return p
}

//go:noinline
func mkVar(c int) *N {
	var x N
	x.v = c
	return &x
}

func mkInl(c int) *N { return &N{v: c} }

//go:noinline
func mkChain(c int) *N { return mkNew(c + 1) }

//go:noinline
func mkList(c int) (p *N) {
	for i := 0; i < 2; i++ {
		p = &N{v: c + i, next: p}
	}
	return
}

//go:noinline
func mkAfter(a *N, c int) *N { return &N{v: c + v(a), next: a} }

//go:noinline
func idS(p *N) *N { return p }

//go:noinline
func pickS(a, b *N) *N {
	if v(a) > v(b) {
		return a
	}
	return b
}

//go:noinline
func setS(p *N, c int) { p.v = c }

//go:noinline
func nextS(p *N) *N {
	if p == nil {
		return nil
	}
	return p.next
}

func v(p *N) int {
	if p == nil {
		return -1
	}
	return p.v
}

func nv(p *N) int {
	if p == nil {
		return -1
	}
	return v(p.next)
}

func tr(tag int, vals ...int) {
	fmt.Println(" ", tag, vals)
}

//go:noinline
func smash(n int) int {
	var buf [64]int
	for i := range buf {
		buf[i] = -1
	}
	if n == 0 {
		return buf[3]
	}
	return smash(n-1) + buf[n%64]
}

// grow makes the stack grow, if it has not already.
//
//go:noinline
func grow(n int) int {
	var buf [256]int
	buf[n%256] = n
	if n == 0 {
		return 0
	}
	return grow(n-1) + buf[n%256]
}

// gcParked collects while the calling goroutine is parked, its stack
// pointer known to the collector.
func gcParked() {
	done := make(chan struct{})
	go func() { runtime.GC(); close(done) }()
	<-done
}

//go:noinline
func deep(n int, f func() *N) *N {
	var pad [100]int
	pad[n] = n
	var p *N
	if n == 0 {
		p = f()
	} else {
		p = deep(n-1, f)
	}
	if pad[n] != n {
		panic("pad")
	}
	return p
}

func chain(p *N) string {
	s := ""
	for i := 0; i < 5; i++ {
		if p == nil {
			return s + "nil"
		}
		s += fmt.Sprint(p.v, p.pad[0], ">")
		p = p.next
	}
	return s + "..."
}

func check(name string, f func() *N) {
	sink = nil
	p := deep(10, f)
	smash(30)
	gcParked()
	all := append([]*N{p}, sink...)
	fmt.Println(name)
	for i, q := range all {
		same := -1
		for j := 0; j < i; j++ {
			if all[j] == q && q != nil {
				same = j
				break
			}
		}
		fmt.Println(" ", i, chain(q), same)
	}
	fmt.Println("  quiet", quietN)
}
`

// An ebrGen generates one program.
type ebrGen struct {
	r   *rand.Rand
	b   bytes.Buffer
	tag int

	// The variables in scope in the function being generated.
	vals    []string // of type N
	ptrs    []string // of type *N
	ints    []string // int expressions: loop variables
	nvar    int
	named   bool // the function has a named result r
	inFunc  bool // not in a closure: return is the function's
	inLoop  int
	ncallee int // generated callees g0..g(ncallee-1) exist
	nbuild  int // generated builders b0..b(nbuild-1) exist
}

func ebrGenerate(seed int64, nfuncs int) []byte {
	g := &ebrGen{r: rand.New(rand.NewSource(seed))}
	g.b.WriteString(ebrPrelude)
	var names []string
	for i := 0; i < nfuncs; i++ {
		if g.r.Intn(3) == 0 {
			g.callee()
		}
		if g.r.Intn(2) == 0 {
			g.builder()
		}
		names = append(names, g.function(i))
	}
	g.b.WriteString("\nfunc main() {\n")
	for _, n := range names {
		fmt.Fprintf(&g.b, "\tcheck(%q, %s)\n", n, n)
	}
	g.b.WriteString("}\n")
	return g.b.Bytes()
}

func (g *ebrGen) p(depth int, format string, args ...any) {
	g.b.WriteString(strings.Repeat("\t", depth))
	fmt.Fprintf(&g.b, format, args...)
	g.b.WriteByte('\n')
}

func (g *ebrGen) pick(s []string) string { return s[g.r.Intn(len(s))] }

func (g *ebrGen) val() string { return g.pick(g.vals) }
func (g *ebrGen) ptr() string { return g.pick(g.ptrs) }

// intExpr returns an int expression.
func (g *ebrGen) intExpr() string {
	switch n := g.r.Intn(6); {
	case n == 0 && len(g.ints) > 0:
		return g.pick(g.ints)
	case n == 1:
		return g.val() + ".v"
	case n == 2:
		return "v(" + g.ptr() + ")"
	}
	return fmt.Sprint(1 + g.r.Intn(9))
}

// ptrExpr returns an expression of type *N that is not a variable's
// address.
func (g *ebrGen) ptrExpr() string {
	c := g.intExpr()
	switch g.r.Intn(22) {
	case 0:
		return fmt.Sprintf("&N{v: %s}", c)
	case 1:
		return fmt.Sprintf("&N{v: %s, next: %s}", c, g.ptr())
	case 2:
		return "new(N)"
	case 3:
		return fmt.Sprintf("mkLit(%s)", c)
	case 4:
		return fmt.Sprintf("mkNew(%s)", c)
	case 5:
		return fmt.Sprintf("mkVar(%s)", c)
	case 6:
		return fmt.Sprintf("mkInl(%s)", c)
	case 7:
		return fmt.Sprintf("mkChain(%s)", c)
	case 8:
		return fmt.Sprintf("mkList(%s)", c)
	case 9:
		return fmt.Sprintf("mkAfter(%s, %s)", g.ptr(), c)
	case 10:
		return fmt.Sprintf("D.mk(%s)", c)
	case 11:
		return fmt.Sprintf("ifaces[%d].Mk(%s)", g.r.Intn(5), c)
	case 12:
		return fmt.Sprintf("idS(%s)", g.ptr())
	case 13:
		return fmt.Sprintf("pickS(%s, %s)", g.ptr(), g.ptr())
	case 14:
		return fmt.Sprintf("nextS(%s)", g.ptr())
	case 15:
		return fmt.Sprintf("D.ret(%s)", g.ptr())
	case 16:
		return fmt.Sprintf("D.ret(&%s)", g.val())
	case 17:
		return fmt.Sprintf("ifaces[%d].Ret(&%s)", g.r.Intn(5), g.val())
	case 18:
		return fmt.Sprintf("D.retNext(&%s)", g.val())
	case 19:
		return fmt.Sprintf("D.retAny(&%s).(*N)", g.val())
	case 20:
		return g.val() + ".next"
	}
	if g.nbuild > 0 && g.r.Intn(4) != 0 {
		return g.buildCall(c)
	}
	if g.ncallee > 0 {
		return fmt.Sprintf("g%d(%s, %s)", g.r.Intn(g.ncallee), g.ptr(), c)
	}
	return g.ptr()
}

func (g *ebrGen) buildCall(c string) string {
	return fmt.Sprintf("b%d(%s, %s)", g.r.Intn(g.nbuild), g.ptr(), c)
}

// builder generates a function b<n>(a *N, c int) (r *N) that returns
// a node it makes, in one of the ways that may or may not let the
// node be made in the caller's buffer.
func (g *ebrGen) builder() {
	name := fmt.Sprintf("b%d", g.nbuild)
	if g.r.Intn(4) != 0 {
		g.b.WriteString("\n//go:noinline")
	}
	g.p(0, "\nfunc %s(a *N, c int) (r *N) {", name)
	alloc := func(d int, v, c string) {
		switch n := g.r.Intn(9); {
		case n == 0:
			g.p(d, "%s := &N{v: %s}", v, c)
		case n == 1:
			g.p(d, "%s := &N{v: %s, next: a}", v, c)
		case n == 2:
			g.p(d, "%s := new(N)", v)
			g.p(d, "%s.v = %s", v, c)
		case n == 3:
			g.p(d, "var x%s N", v)
			g.p(d, "x%s.v = %s", v, c)
			g.p(d, "%s := &x%s", v, v)
		case n == 4:
			g.p(d, "%s := mkLit(%s)", v, c)
		case n == 5:
			g.p(d, "%s := D.mk(%s)", v, c)
		case n == 6:
			g.p(d, "%s := ifaces[%d].Mk(%s)", v, 2*g.r.Intn(2), c)
		case g.nbuild > 0:
			g.p(d, "%s := b%d(a, %s)", v, g.r.Intn(g.nbuild), c)
			g.p(d, "if %s == nil {", v)
			g.p(d+1, "%s = new(N)", v)
			g.p(d, "}")
		default:
			g.p(d, "%s := mkNew(%s)", v, c)
		}
	}
	use := func(d int, v string) {
		for i := g.r.Intn(4); i > 0; i-- {
			g.tag++
			switch g.r.Intn(16) {
			case 0:
				g.p(d, "%s.v += v(a)", v)
			case 1:
				g.p(d, "%s.next = a", v)
			case 2:
				g.p(d, "%s.pad[0] = c", v)
			case 3:
				g.p(d, "tr(%d, %s.v, nv(%s))", g.tag, v, v)
			case 4:
				g.p(d, "setS(%s, %s.v+1)", v, v)
			case 5:
				g.p(d, "if c%%2 == 0 {")
				g.p(d+1, "%s.v++", v)
				g.p(d, "}")
			case 6:
				g.p(d, "grow(%d)", 20+g.r.Intn(40))
			case 7:
				g.p(d, "gcParked()")
			case 8:
				g.p(d, "%s = idS(%s)", v, v)
			case 9:
				g.p(d, "%s.v += D.peek(%s)", v, v)
			case 10:
				g.p(d, "sink = append(sink, %s)", v)
			case 11:
				g.p(d, "var h%d N", g.tag)
				g.p(d, "h%d.next = %s", g.tag, v)
				g.p(d, "%s.v += v(h%d.next)", v, g.tag)
			case 12:
				g.p(d, "%s.next = %s", v, v)
			case 13:
				g.p(d, "if a != nil {")
				g.p(d+1, "a.v += %s.v", v)
				g.p(d, "}")
			case 14:
				g.p(d, "func() { %s.v += 2 }()", v)
			case 15:
				g.p(d, "%s.v += len(fmt.Sprint(%s.v))", v, v)
			}
		}
	}
	switch g.r.Intn(12) {
	case 0, 1, 2:
		alloc(1, "p", "c")
		use(1, "p")
		g.p(1, "return p")
	case 3:
		alloc(1, "p", "c")
		use(1, "p")
		g.p(1, "r = p")
		use(1, "r")
		g.p(1, "return")
	case 4:
		g.p(1, "if c%%3 == 0 {")
		alloc(2, "p", "c")
		use(2, "p")
		g.p(2, "return p")
		g.p(1, "}")
		alloc(1, "q", "c+1")
		use(1, "q")
		g.p(1, "return q")
	case 5:
		alloc(1, "p", "c")
		use(1, "p")
		g.p(1, "return pickS(p, a)")
	case 6:
		g.p(1, "for i := 0; i < 2+c%%2; i++ {")
		alloc(2, "p", "c+i")
		use(2, "p")
		switch g.r.Intn(3) {
		case 0:
			g.p(2, "p.next = r")
		case 1:
			g.p(2, "p.v += v(r)")
		}
		g.p(2, "r = p")
		g.p(1, "}")
		g.p(1, "return")
	case 7:
		g.p(1, "for i := 0; i < 3; i++ {")
		alloc(2, "p", "c+i")
		use(2, "p")
		g.p(2, "if i == c%%3 {")
		g.p(3, "return p")
		g.p(2, "}")
		g.p(1, "}")
		g.p(1, "return nil")
	case 8:
		g.p(1, "var best *N")
		g.p(1, "for i := 0; i < 3; i++ {")
		alloc(2, "p", "(c*(i+3))%5")
		use(2, "p")
		g.p(2, "if best == nil || p.v > best.v {")
		g.p(3, "best = p")
		g.p(2, "}")
		g.p(1, "}")
		g.p(1, "return best")
	case 9:
		g.p(1, "defer func() {")
		g.p(2, "if r != nil {")
		g.p(3, "r.pad[1]++")
		g.p(2, "}")
		panics := g.r.Intn(2) == 0
		if panics {
			g.p(2, "recover()")
		}
		g.p(1, "}()")
		alloc(1, "p", "c")
		use(1, "p")
		g.p(1, "r = p")
		if panics {
			g.p(1, "if c%%4 == 1 {")
			g.p(2, "panic(c)")
			g.p(1, "}")
		}
		g.p(1, "return r")
	case 10:
		alloc(1, "p", "c")
		alloc(1, "q", "c+2")
		use(1, "p")
		use(1, "q")
		switch g.r.Intn(3) {
		case 0:
			g.p(1, "p.next = q")
			g.p(1, "return p")
		case 1:
			g.p(1, "if p.v > q.v+1 {")
			g.p(2, "return p")
			g.p(1, "}")
			g.p(1, "return q")
		case 2:
			g.p(1, "q.v += p.v")
			g.p(1, "return q")
		}
	case 11:
		g.p(1, "var hold [2]*N")
		g.p(1, "for i := range hold {")
		alloc(2, "p", "c+i")
		use(2, "p")
		g.p(2, "hold[i] = p")
		g.p(1, "}")
		g.p(1, "hold[1].v += hold[0].v")
		g.p(1, "return hold[c&1]")
	}
	g.p(0, "}")
	g.nbuild++
}

func (g *ebrGen) newVal(d int) {
	name := fmt.Sprintf("x%d", g.nvar)
	g.nvar++
	switch g.r.Intn(3) {
	case 0:
		g.p(d, "var %s N", name)
		g.p(d, "%s.v = %s", name, g.intExpr())
	case 1:
		g.p(d, "%s := N{v: %s}", name, g.intExpr())
	case 2:
		g.p(d, "%s := N{v: %s, next: %s}", name, g.intExpr(), g.ptr())
	}
	g.p(d, "_ = %s", name)
	g.vals = append(g.vals, name)
}

func (g *ebrGen) newPtr(d int) {
	name := fmt.Sprintf("p%d", g.nvar)
	g.nvar++
	if g.r.Intn(2) == 0 {
		g.p(d, "var %s *N", name)
	} else {
		g.p(d, "%s := %s", name, g.ptrExpr())
	}
	g.p(d, "_ = %s", name)
	g.ptrs = append(g.ptrs, name)
}

// block generates n statements in a new scope.
func (g *ebrGen) block(d, n, nest int) {
	nv, np, ni := len(g.vals), len(g.ptrs), len(g.ints)
	for i := 0; i < n; i++ {
		g.stmt(d, nest)
	}
	g.vals, g.ptrs, g.ints = g.vals[:nv], g.ptrs[:np], g.ints[:ni]
}

func (g *ebrGen) stmt(d, nest int) {
	x, p, q := g.val(), g.ptr(), g.ptr()
	c := g.intExpr()
	g.tag++
	switch n := g.r.Intn(46); n {
	case 0, 1:
		g.newVal(d)
	case 2:
		g.newPtr(d)
	case 3:
		g.p(d, "%s.v = %s", x, c)
	case 4:
		g.p(d, "%s = &%s", p, x)
	case 5, 6, 7, 8, 9:
		if g.nbuild > 0 && g.r.Intn(3) == 0 {
			g.p(d, "%s = %s", p, g.buildCall(c))
		} else {
			g.p(d, "%s = %s", p, g.ptrExpr())
		}
	case 10, 11:
		g.p(d, "D.keep(&%s)", x)
	case 12:
		g.p(d, "ifaces[%d].Keep(&%s)", g.r.Intn(5), x)
	case 13:
		g.p(d, "D.keep(%s)", p)
	case 14:
		g.p(d, "%s.v += D.peek(&%s)", x, g.val())
	case 15:
		g.p(d, "%s.v += ifaces[%d].Peek(&%s)", x, g.r.Intn(5), g.val())
	case 16:
		g.p(d, "D.keep2nd(&%s, &%s)", x, g.val())
	case 17:
		g.p(d, "D.link(&%s, %s)", x, p)
	case 18:
		g.p(d, "if %s != nil {", p)
		g.p(d+1, "D.link(%s, &%s)", p, x)
		g.p(d, "}")
	case 19:
		g.p(d, "if %s != nil {", p)
		g.p(d+1, "%s.next = %s", p, q)
		g.p(d, "}")
	case 20:
		g.p(d, "if %s != nil {", p)
		g.p(d+1, "%s.v += %s", p, c)
		g.p(d+1, "%s.pad[0]++", p)
		g.p(d, "}")
	case 21:
		g.p(d, "%s.next = %s", x, p)
	case 22:
		g.p(d, "if %s != nil {", p)
		g.p(d+1, "%s = %s.next", q, p)
		g.p(d, "}")
	case 23:
		g.p(d, "%s = %s", p, q)
	case 24, 25:
		g.p(d, "sink = append(sink, %s)", p)
	case 26, 27, 28:
		g.p(d, "tr(%d, %s.v, v(%s), nv(%s), v(%s.next))", g.tag, x, p, p, x)
	case 29:
		g.p(d, "%s = N{v: %s, next: %s}", x, c, p)
	case 30:
		g.p(d, "if %s != nil {", p)
		g.p(d+1, "%s = *%s", x, p)
		g.p(d, "}")
	case 31:
		g.p(d, "setS(&%s, %s)", x, c)
	case 32:
		g.p(d, "setS(&%s, D.peek(&%s)+ifaces[%d].Peek(&%s))", x, x, g.r.Intn(5), g.val())
	case 33:
		if g.named && g.inFunc {
			g.p(d, "r = %s", g.ptrExpr())
		} else {
			g.p(d, "%s = %s", p, g.ptrExpr())
		}
	case 34:
		if g.named {
			g.p(d, "%s = r", p)
		}
	case 35:
		if nest > 0 && g.inFunc {
			g.p(d, "if %s > %d {", c, 4+g.r.Intn(8))
			if g.named {
				if g.r.Intn(2) == 0 {
					g.p(d+1, "return")
				} else {
					g.p(d+1, "return %s", g.ptrExpr())
				}
			} else {
				g.p(d+1, "return %s", g.ptrExpr())
			}
			g.p(d, "}")
		}
	case 36:
		g.p(d, "gcParked()")
	case 37:
		g.p(d, "grow(%d)", 20+g.r.Intn(60))
	case 38, 39, 40:
		if nest < 3 {
			i := fmt.Sprintf("i%d", g.nvar)
			g.nvar++
			if g.r.Intn(4) == 0 {
				g.p(d, "for %s := range %d {", i, 2+g.r.Intn(2))
			} else {
				g.p(d, "for %s := 0; %s < %d; %s++ {", i, i, 2+g.r.Intn(2), i)
			}
			g.p(d+1, "_ = %s", i)
			ni := len(g.ints)
			g.ints = append(g.ints, i)
			g.inLoop++
			g.block(d+1, 1+g.r.Intn(5), nest+1)
			g.inLoop--
			g.ints = g.ints[:ni]
			g.p(d, "}")
		}
	case 41, 42:
		if nest < 3 {
			g.p(d, "if %s%%2 == 0 {", c)
			g.block(d+1, 1+g.r.Intn(4), nest+1)
			if g.r.Intn(2) == 0 {
				g.p(d, "} else {")
				g.block(d+1, 1+g.r.Intn(3), nest+1)
			}
			g.p(d, "}")
		}
	case 43, 44, 45:
		if nest < 3 {
			inFunc := g.inFunc
			g.inFunc = false
			kind := g.r.Intn(4)
			if kind == 3 && g.inLoop > 0 {
				kind = 0 // no defers in loops: they run in another order
			}
			switch kind {
			case 0:
				g.p(d, "func() {")
			case 1:
				g.p(d, "D.call(func() {")
			case 2:
				g.p(d, "func() {")
				g.p(d+1, "defer func() {}()")
			case 3:
				g.p(d, "defer func() {")
			}
			g.block(d+1, 1+g.r.Intn(4), nest+1)
			if kind == 1 {
				g.p(d, "})")
			} else {
				g.p(d, "}()")
			}
			g.inFunc = inFunc
		}
	}
}

// function generates test function number i and returns its name.
func (g *ebrGen) function(i int) string {
	name := fmt.Sprintf("t%d", i)
	g.vals, g.ptrs, g.ints, g.nvar = nil, nil, nil, 0
	g.named = g.r.Intn(2) == 0
	g.inFunc, g.inLoop = true, 0
	g.b.WriteString("\n//go:noinline\n")
	if g.named {
		g.p(0, "func %s() (r *N) {", name)
	} else {
		g.p(0, "func %s() *N {", name)
	}
	g.body(false)
	g.p(0, "}")
	return name
}

// callee generates a function g<n>(a *N, c int) *N that test
// functions and later callees call.
func (g *ebrGen) callee() {
	name := fmt.Sprintf("g%d", g.ncallee)
	g.vals, g.ptrs, g.ints, g.nvar = nil, []string{"a"}, []string{"c"}, 0
	g.named = g.r.Intn(2) == 0
	g.inFunc, g.inLoop = true, 0
	if g.r.Intn(3) != 0 {
		g.b.WriteString("\n//go:noinline")
	}
	if g.named {
		g.p(0, "\nfunc %s(a *N, c int) (r *N) {", name)
	} else {
		g.p(0, "\nfunc %s(a *N, c int) *N {", name)
	}
	g.body(true)
	g.p(0, "}")
	g.ncallee++
}

func (g *ebrGen) body(small bool) {
	// The variables ptrExpr and intExpr need.
	g.p(1, "var x0 N")
	g.p(1, "x0.v = %d", 1+g.r.Intn(9))
	g.p(1, "var p0 *N")
	g.p(1, "_, _ = x0, p0")
	g.vals = append(g.vals, "x0")
	g.ptrs = append(g.ptrs, "p0")
	g.nvar = 1
	n := 4 + g.r.Intn(12)
	if small {
		n = 2 + g.r.Intn(6)
	}
	for i := 0; i < n; i++ {
		g.stmt(1, 0)
	}
	switch {
	case g.named && g.r.Intn(2) == 0:
		g.p(1, "return")
	case g.r.Intn(4) == 0:
		g.p(1, "return &%s", g.val())
	default:
		g.p(1, "return %s", g.ptr())
	}
}
