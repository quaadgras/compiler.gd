// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The typed syntax tree. The parser checks and types every expression,
// making C's implicit conversions explicit (cast nodes), decaying arrays
// and functions, and folding constants, so that generating Go needs no
// more of C's rules.

type pos struct {
	file string
	line int
}

type expr interface {
	ty() *ctype
	at() pos
}

type node struct {
	t *ctype
	p pos
}

func (n *node) ty() *ctype { return n.t }
func (n *node) at() pos    { return n.p }

type (
	// An integer or pointer constant (a null pointer is 0).
	intConst struct {
		node
		v int64
	}
	floatConst struct {
		node
		v float64
	}
	// A complex constant (from GNU's imaginary constants).
	complexConst struct {
		node
		re, im float64
	}
	// A string literal: an array (t) of its elements, data the bytes.
	strLit struct {
		node
		data string
	}
	// A variable or function.
	varRef struct {
		node
		sym *symbol
	}
	// -x, ~x, !x (op "!" gives int)
	unary struct {
		node
		op string
		x  expr
	}
	// &x: x is an lvalue (or a function).
	addrOf struct {
		node
		x expr
	}
	// *x: x is a pointer; an lvalue.
	deref struct {
		node
		x expr
	}
	// x op y, after the usual arithmetic conversions; for pointer
	// arithmetic, x is the pointer and y an integer (or both pointers,
	// for -); comparisons give int; "," and "&&", "||".
	binary struct {
		node
		op   string
		x, y expr
	}
	// x = y (op "="), or x op= y: computed in ct, then converted to x's type.
	assign struct {
		node
		op   string
		x, y expr
		ct   *ctype
	}
	// ++x, x++, --x, x--.
	incDec struct {
		node
		op   string // "++" or "--"
		post bool
		x    expr
	}
	cond struct {
		node
		c, x, y expr
	}
	call struct {
		node
		fn   expr // a function designator or a pointer to a function
		ft   *funcType
		args []expr // converted to the parameters' types
	}
	// x.f (x an lvalue or a struct value) or, after the parser's rewrite,
	// (*p).f for p->f.
	member struct {
		node
		x expr
		f *field
	}
	// A conversion to t.
	cast struct {
		node
		x expr
	}
	// An array's (or function's) value as a pointer to its first element.
	decay struct {
		node
		x expr
	}
	// (T){...}: an unnamed object; an lvalue.
	compoundLit struct {
		node
		sym *symbol
	}
	// ({ ... }): the value of the last expression statement, if any.
	stmtExpr struct {
		node
		body *block
		last expr
	}
	// sizeof of a variable length array: computed at run time.
	vlaSizeof struct {
		node
		of *ctype
	}
	// __builtin_va_start(ap), _arg(ap, T) (t is T), _end(ap), _copy(d, s).
	vaOp struct {
		node
		op  string
		ap  expr
		src expr
	}
	// Statements, then x: the lengths of variable length array types
	// that an expression names (sizeof(int[n])).
	seqExpr struct {
		node
		pre []stmt
		x   expr
	}
	// &&l: a label's address, GNU's.
	labelAddr struct {
		node
		l *label
	}
	// A Go expression the generator made: in an expression it builds.
	goVal struct {
		node
		code string
	}
	// A call of a built-in function, cmd/cc's own.
	builtinCall struct {
		node
		name string
		args []expr
	}
)

// An initializer, flattened: the stores it makes into its object.
type initItem struct {
	off  int64
	t    *ctype
	f    *field // a bit-field, if it stores one
	x    expr   // a scalar, struct value, or string literal for an array
	zero bool   // zero t's bytes at off first (a union member's padding)
}

type initializer struct {
	items  []initItem
	scalar expr // for a scalar not in memory
}

type storage uint8

const (
	sAuto storage = iota
	sStatic
	sExtern
	sTypedef
	sRegister
)

type symKind uint8

const (
	symVar symKind = iota
	symFunc
	symTypedef
	symEnumConst
)

type symbol struct {
	name      string
	kind      symKind
	t         *ctype
	storage   storage
	global    bool // file scope, or static storage duration
	linkage   byte // 'e' external, 'i' internal, 0 none
	tls       bool
	addr      bool // its address is taken: in memory
	init      *initializer
	val       int64 // symEnumConst; a constexpr's value
	constx    bool  // constexpr
	cval      expr  // a constexpr's (folded) value
	defined   bool
	fn        *funcDecl
	goName    string
	p         pos
	used      bool
	isParam   bool
	weak      bool    // __attribute__((weak)): null if not defined
	cleanup   *symbol // __attribute__((cleanup(f))): f(&it) when its block is left
	nested    bool    // GNU's nested function
	vlaSize   string  // the Go temp with a VLA's size in bytes
	allocSize int64   // more than its type\'s size: a flexible array member\'s initializer
}

type funcDecl struct {
	sym     *symbol
	params  []*symbol
	body    *block
	locals  []*symbol
	labels  map[string]*label
	hasGoto bool
	usesVA  bool
	inline  bool
	setjmps int // calls of setjmp: their sites are 1...setjmps

	labelIDs   int
	addrLabels []*label // labels whose address is taken
}

type label struct {
	id    int // its address's value, if taken (&&l)
	local int // GNU's local label: a number to tell it from others
	name  string
	used  bool
	p     pos
}

type stmt interface{}

type (
	block struct {
		list []stmt
	}
	exprStmt struct {
		x expr
	}
	declStmt struct {
		syms []*symbol // with their initializers
	}
	ifStmt struct {
		c         expr
		then, els stmt
	}
	whileStmt struct {
		c    expr
		body stmt
	}
	doStmt struct {
		body stmt
		c    expr
	}
	forStmt struct {
		init stmt // a declStmt or exprStmt, or nil
		c    expr // nil: forever
		post expr
		body stmt
	}
	switchStmt struct {
		x     expr
		body  stmt
		cases []*caseStmt
		dflt  *caseStmt
	}
	caseStmt struct {
		lo, hi int64 // the values it matches (hi: GNU's case ranges)
		dflt   bool
		body   stmt
		sw     *switchStmt
		id     int
	}
	breakStmt struct {
		label string // C2y: break label
	}
	continueStmt struct {
		label string
	}
	returnStmt struct {
		x expr
	}
	gotoStmt struct {
		l *label
		x expr // goto *x
	}
	// goto *p: not supported.
	labelStmt struct {
		l    *label
		body stmt
	}
	emptyStmt struct{}
	// GNU's nested function: defined here, a closure.
	nestedFuncStmt struct {
		f *funcDecl
	}
	// C2y's defer: body runs when the block is left.
	deferStmt struct {
		body stmt
	}
)

// A unit is a parsed translation unit.
type unit struct {
	file    string
	syms    []*symbol // file-scope variables and functions, in order
	funcs   []*funcDecl
	records []*ctype  // the struct and union types, in order of completion
	statics []*symbol // block-scope statics, and compound literals at file scope
	tags    map[string]*ctype
	scope   map[string]*symbol // file scope: for cgo's names
}

// goName is a label's Go name, without its L_.
func (l *label) goName() string {
	if l.local > 0 {
		return goIdent(l.name) + "_" + itoa(int64(l.local))
	}
	return goIdent(l.name)
}
