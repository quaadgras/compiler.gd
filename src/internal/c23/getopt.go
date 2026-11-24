// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"os"
	"strings"
	"sync"
	"unsafe"
)

// getopt's globals, which unistd.h's optarg, optind, opterr and optopt
// name through the pointers OptargLoc and the others return.
var (
	OptargObj = New[unsafe.Pointer]()
	OptindObj = newInt32(1)
	OpterrObj = newInt32(1)
	OptoptObj = newInt32('?')
)

func newInt32(v int32) Ptr {
	p := New[int32]()
	Store32(p, uint32(v))
	return p
}

// getopt's state between calls, as glibc's.
type getoptStateT struct {
	sync.Mutex
	init        bool
	nextchar    Ptr // the rest of the current argument's options
	ordering    int
	firstNonopt int32
	lastNonopt  int32
}

var getoptState getoptStateT

const (
	requireOrder  = iota // stop at the first non-option (+, POSIXLY_CORRECT)
	permute              // move the non-options to the end (the default)
	returnInOrder        // return non-options as the option 1 (-)
)

// getopt is getopt, getopt_long (longopts not null) and getopt_long_only.
func Getopt(argc int32, argv, optstring, longopts, longind Ptr, longOnly bool) int32 {
	st := &getoptState
	st.Lock()
	defer st.Unlock()
	arg := func(i int32) Ptr { return LoadPtr(Add(argv, int(i)*int(WordSize))) }
	optind := int32(Load32(OptindObj))
	setOptind := func(i int32) { optind = i; Store32(OptindObj, uint32(i)) }
	StorePtr(OptargObj, Ptr(nil))
	if argc < 1 {
		return -1
	}
	opts := GoString(optstring)
	if optind == 0 || !st.init {
		if optind == 0 {
			setOptind(1)
		}
		st.firstNonopt, st.lastNonopt = optind, optind
		st.nextchar = Ptr(nil)
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
	printErrors := Load32(OpterrObj) != 0
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
			pi, pj := Add(argv, int(i)*int(WordSize)), Add(argv, int(j)*int(WordSize))
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
			StorePtr(OptargObj, arg(optind))
			setOptind(optind + 1)
			return 1
		}
		if Addr(longopts) != 0 {
			a := arg(optind)
			if Load8(Add(a, 1)) == '-' {
				st.nextchar = Add(a, 2)
				return st.longOption(argc, arg, opts, longopts, longind, longOnly, printErrors, "--", optind, setOptind)
			}
			if longOnly && (Load8(Add(a, 2)) != 0 || strings.IndexByte(opts, Load8(Add(a, 1))) < 0) {
				st.nextchar = Add(a, 1)
				if c := st.longOption(argc, arg, opts, longopts, longind, longOnly, printErrors, "-", optind, setOptind); c != -1 {
					return c
				}
			}
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
		Store32(OptoptObj, uint32(c))
		return '?'
	}
	if i+1 < len(opts) && opts[i+1] == ':' {
		if i+2 < len(opts) && opts[i+2] == ':' {
			// An optional argument: only in the same argv element.
			if Load8(st.nextchar) != 0 {
				StorePtr(OptargObj, st.nextchar)
				setOptind(optind + 1)
			}
		} else if Load8(st.nextchar) != 0 {
			StorePtr(OptargObj, st.nextchar)
			setOptind(optind + 1)
		} else if optind == argc {
			if printErrors {
				getoptError("option requires an argument", arg(0), c)
			}
			Store32(OptoptObj, uint32(c))
			if len(opts) > 0 && opts[0] == ':' {
				return ':'
			}
			return '?'
		} else {
			StorePtr(OptargObj, arg(optind))
			setOptind(optind + 1)
		}
		st.nextchar = Ptr(nil)
	}
	return int32(c)
}

// getoptError writes getopt's message, as glibc words it, on stderr.
func getoptError(msg string, prog Ptr, c byte) {
	FileOf(Stdio(2)).Write([]byte(GoString(prog) + ": " + msg + " -- '" + string([]byte{c}) + "'\n"))
}

// option is C's struct option.
type option struct {
	name   unsafe.Pointer
	hasArg int32
	flag   unsafe.Pointer
	val    int32
}

// longOption is glibc's process_long_option: the long option at
// st.nextchar, which prefix ("--" or "-") came before, in argv[optind].
// It returns -1 if getopt_long_only should take it as short options.
func (st *getoptStateT) longOption(argc int32, arg func(int32) Ptr, opts string, longopts, longind Ptr, longOnly, printErrors bool, prefix string, optind int32, setOptind func(int32)) int32 {
	var o option
	osize := int(unsafe.Sizeof(o))
	offHas, offFlag, offVal := int(unsafe.Offsetof(o.hasArg)), int(unsafe.Offsetof(o.flag)), int(unsafe.Offsetof(o.val))
	rest := GoString(st.nextchar)
	name, _, hasValue := strings.Cut(rest, "=")
	type opt struct {
		name        string
		hasArg, val int32
		flag        Ptr
		idx         int
	}
	var all []opt
	for i := 0; ; i++ {
		e := Add(longopts, i*osize)
		np := LoadPtr(e)
		if Addr(np) == 0 {
			break
		}
		all = append(all, opt{GoString(np), int32(Load32(Add(e, offHas))), int32(Load32(Add(e, offVal))), LoadPtr(Add(e, offFlag)), i})
	}
	var found *opt
	for i := range all {
		if all[i].name == name {
			found = &all[i]
			break
		}
	}
	if found == nil {
		ambig := false
		for i := range all {
			p := &all[i]
			if !strings.HasPrefix(p.name, name) {
				continue
			}
			if found == nil {
				found = p
			} else if longOnly || found.hasArg != p.hasArg || Addr(found.flag) != Addr(p.flag) || found.val != p.val {
				ambig = true
			}
		}
		if ambig {
			if printErrors {
				msg := GoString(arg(0)) + ": option '" + prefix + rest + "' is ambiguous; possibilities:"
				for _, p := range all {
					if strings.HasPrefix(p.name, name) {
						msg += " '" + prefix + p.name + "'"
					}
				}
				FileOf(Stdio(2)).Write([]byte(msg + "\n"))
			}
			st.nextchar = Ptr(nil)
			setOptind(optind + 1)
			Store32(OptoptObj, 0)
			return '?'
		}
	}
	if found == nil {
		if !longOnly || Load8(Add(arg(optind), 1)) == '-' || strings.IndexByte(opts, Load8(st.nextchar)) < 0 {
			if printErrors {
				FileOf(Stdio(2)).Write([]byte(GoString(arg(0)) + ": unrecognized option '" + prefix + rest + "'\n"))
			}
			st.nextchar = Ptr(nil)
			setOptind(optind + 1)
			Store32(OptoptObj, 0)
			return '?'
		}
		return -1
	}
	optind++
	setOptind(optind)
	st.nextchar = Ptr(nil)
	if hasValue {
		if found.hasArg != 0 {
			StorePtr(OptargObj, Add(arg(optind-1), len(prefix)+len(name)+1))
		} else {
			if printErrors {
				FileOf(Stdio(2)).Write([]byte(GoString(arg(0)) + ": option '" + prefix + found.name + "' doesn't allow an argument\n"))
			}
			Store32(OptoptObj, uint32(found.val))
			return '?'
		}
	} else if found.hasArg == 1 {
		if optind < argc {
			StorePtr(OptargObj, arg(optind))
			setOptind(optind + 1)
		} else {
			if printErrors {
				FileOf(Stdio(2)).Write([]byte(GoString(arg(0)) + ": option '" + prefix + found.name + "' requires an argument\n"))
			}
			Store32(OptoptObj, uint32(found.val))
			if len(opts) > 0 && opts[0] == ':' {
				return ':'
			}
			return '?'
		}
	}
	if Addr(longind) != 0 {
		Store32(longind, uint32(found.idx))
	}
	if Addr(found.flag) != 0 {
		Store32(found.flag, uint32(found.val))
		return 0
	}
	return found.val
}
