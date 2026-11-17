// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"sync"
	"unsafe"
)

// getopt's globals, which unistd.h's optarg, optind, opterr and optopt
// name through the pointers OptargLoc and the others return.
var (
	optargObj = New[unsafe.Pointer]()
	optindObj = newInt32(1)
	opterrObj = newInt32(1)
	optoptObj = newInt32('?')
)

func newInt32(v int32) Ptr {
	p := New[int32]()
	Store32(p, uint32(v))
	return p
}

func OptargLoc() Ptr { return optargObj }
func OptindLoc() Ptr { return optindObj }
func OpterrLoc() Ptr { return opterrObj }
func OptoptLoc() Ptr { return optoptObj }

// getopt's state between calls, as glibc's.
var getoptState struct {
	sync.Mutex
	init        bool
	nextchar    Ptr // the rest of the current argument's options
	ordering    int
	firstNonopt int32
	lastNonopt  int32
}

const (
	requireOrder  = iota // stop at the first non-option (+, POSIXLY_CORRECT)
	permute              // move the non-options to the end (the default)
	returnInOrder        // return non-options as the option 1 (-)
)

// Getopt is glibc's getopt: it permutes argv so that the non-options come
// last, unless optstring starts with + or POSIXLY_CORRECT is set; with -
// it returns each non-option as the option 1, in optarg. Setting optind to
// 0 starts over.
func Getopt(argc int32, argv, optstring Ptr) int32 {
	st := &getoptState
	st.Lock()
	defer st.Unlock()
	arg := func(i int32) Ptr { return LoadPtr(Add(argv, int(i)*8)) }
	optind := int32(Load32(optindObj))
	setOptind := func(i int32) { optind = i; Store32(optindObj, uint32(i)) }
	StorePtr(optargObj, Ptr{})
	if argc < 1 {
		return -1
	}
	opts := GoString(optstring)
	if optind == 0 || !st.init {
		if optind == 0 {
			setOptind(1)
		}
		st.firstNonopt, st.lastNonopt = optind, optind
		st.nextchar = Ptr{}
		switch {
		case len(opts) > 0 && opts[0] == '-':
			st.ordering = returnInOrder
		case len(opts) > 0 && opts[0] == '+':
			st.ordering = requireOrder
		case os.Getenv("POSIXLY_CORRECT") != "":
			st.ordering = requireOrder
		default:
			st.ordering = permute
		}
		st.init = true
	}
	if len(opts) > 0 && (opts[0] == '-' || opts[0] == '+') {
		opts = opts[1:]
	}
	printErrors := Load32(opterrObj) != 0
	if len(opts) > 0 && opts[0] == ':' {
		printErrors = false
	}
	nonopt := func() bool {
		a := arg(optind)
		return Load8(a) != '-' || Load8(Add(a, 1)) == 0
	}
	// exchange swaps the non-options [firstNonopt, lastNonopt) with the
	// options [lastNonopt, optind) that followed them.
	exchange := func() {
		bottom, middle, top := st.firstNonopt, st.lastNonopt, optind
		swap := func(i, j int32) {
			pi, pj := Add(argv, int(i)*8), Add(argv, int(j)*8)
			a, b := LoadPtr(pi), LoadPtr(pj)
			StorePtr(pi, b)
			StorePtr(pj, a)
		}
		for top > middle && middle > bottom {
			if top-middle > middle-bottom {
				n := middle - bottom
				for i := range n {
					swap(bottom+i, top-n+i)
				}
				top -= n
			} else {
				n := top - middle
				for i := range n {
					swap(bottom+i, middle+i)
				}
				bottom += n
			}
		}
		st.firstNonopt += optind - st.lastNonopt
		st.lastNonopt = optind
	}

	if Addr(st.nextchar) == 0 || Load8(st.nextchar) == 0 {
		st.lastNonopt = min(st.lastNonopt, optind)
		st.firstNonopt = min(st.firstNonopt, optind)
		if st.ordering == permute {
			if st.firstNonopt != st.lastNonopt && st.lastNonopt != optind {
				exchange()
			} else if st.lastNonopt != optind {
				st.firstNonopt = optind
			}
			for optind < argc && nonopt() {
				setOptind(optind + 1)
			}
			st.lastNonopt = optind
		}
		if optind != argc && GoString(arg(optind)) == "--" {
			setOptind(optind + 1)
			if st.firstNonopt != st.lastNonopt && st.lastNonopt != optind {
				exchange()
			} else if st.firstNonopt == st.lastNonopt {
				st.firstNonopt = optind
			}
			st.lastNonopt = argc
			setOptind(argc)
		}
		if optind == argc {
			if st.firstNonopt != st.lastNonopt {
				setOptind(st.firstNonopt)
			}
			return -1
		}
		if nonopt() {
			if st.ordering == requireOrder {
				return -1
			}
			StorePtr(optargObj, arg(optind))
			setOptind(optind + 1)
			return 1
		}
		st.nextchar = Add(arg(optind), 1)
	}

	c := Load8(st.nextchar)
	st.nextchar = Add(st.nextchar, 1)
	i := -1
	for j := 0; j < len(opts); j++ {
		if opts[j] == c {
			i = j
			break
		}
	}
	if Load8(st.nextchar) == 0 {
		setOptind(optind + 1)
	}
	if i < 0 || c == ':' || c == ';' {
		if printErrors {
			getoptError("invalid option", arg(0), c)
		}
		Store32(optoptObj, uint32(c))
		return '?'
	}
	if i+1 < len(opts) && opts[i+1] == ':' {
		if i+2 < len(opts) && opts[i+2] == ':' {
			// An optional argument: only in the same argv element.
			if Load8(st.nextchar) != 0 {
				StorePtr(optargObj, st.nextchar)
				setOptind(optind + 1)
			}
		} else if Load8(st.nextchar) != 0 {
			StorePtr(optargObj, st.nextchar)
			setOptind(optind + 1)
		} else if optind == argc {
			if printErrors {
				getoptError("option requires an argument", arg(0), c)
			}
			Store32(optoptObj, uint32(c))
			if len(opts) > 0 && opts[0] == ':' {
				return ':'
			}
			return '?'
		} else {
			StorePtr(optargObj, arg(optind))
			setOptind(optind + 1)
		}
		st.nextchar = Ptr{}
	}
	return int32(c)
}

// getoptError writes getopt's message, as glibc words it, on stderr.
func getoptError(msg string, prog Ptr, c byte) {
	fileOf(Stdio(2)).write([]byte(GoString(prog) + ": " + msg + " -- '" + string([]byte{c}) + "'\n"))
}
