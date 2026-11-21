// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"internal/buildcfg"
	"regexp"
	"strings"
)

// The data model: how big C's pointers and longs are on the target, and
// so how cmd/cc lays C out and which Go types hold C's values there
// (doc/gd/cc.md, "C on wasm32"). It is chosen here, once, from the word
// size of the target as internal/buildcfg gives it to every tool, and
// everything else reads target: the sizes of the types (sizeof), their Go
// types (goType), the predefined macros, the headers' typedefs and the
// library functions whose Go depends on the width of long.
//
//   - LP64 (pointers and long of 8 bytes) on every target but one;
//   - ILP32 on wasm with 4-byte words (GOWASM=gc,split, what GOOS=js
//     means: wasm32), as clang's --target=wasm32 lays C out: pointers,
//     long, size_t of 4 bytes; long long and double of 8, aligned to 8,
//     as Go aligns int64 and float64 there, so that a C struct and the Go
//     struct that mirrors it have the same layout.
//
// The size of a pointer and of long are kept apart (a wasm with 64-bit
// memory would have both of 8, but other models differ). The type of an
// address is Go's uintptr in the code cmd/cc generates (c23.Addr,
// c23.FromInt), whatever the sizes.
type dataModel struct {
	ptrSize  int64 // a pointer in memory (and nullptr_t, va_list)
	longSize int64 // long and unsigned long, so size_t, ptrdiff_t, intptr_t
}

var target = modelFor(buildcfg.GOARCH, buildcfg.GOWASM.PtrSize())

func modelFor(goarch string, wasmPtrSize int) dataModel {
	if goarch == "wasm" && wasmPtrSize == 4 {
		return dataModel{ptrSize: 4, longSize: 4}
	}
	return dataModel{ptrSize: 8, longSize: 8}
}

func (m dataModel) ilp32() bool { return m.longSize == 4 }

func init() {
	if !target.ilp32() {
		return
	}
	for name, edits := range ilp32Headers {
		h := sysHeaders[name]
		for i := 0; i < len(edits); i += 2 {
			if !strings.Contains(h, edits[i]) {
				panic(fmt.Sprintf("cmd/cc: %s has no %q to make ILP32", name, edits[i]))
			}
			h = strings.ReplaceAll(h, edits[i], edits[i+1])
		}
		sysHeaders[name] = h
	}
	sysHeaders["inttypes.h"] = ilp32Formats(sysHeaders["inttypes.h"])
	for c, goName := range libcLong32 {
		libc[c] = goName
	}
	// cgo's C.long and C.ulong, which are Go's int32 and uint32 on
	// 32-bit targets.
	cgoBasic["long"], cgoBasic["ulong"] = goType(tLong), goType(tULong)
}

// ilp32Macros are the predefined macros that differ from LP64's
// (pp.go's predefined), as clang's --target=wasm32 defines them but for
// the types the C library chooses (ilp32Headers).
const ilp32Macros = `
#undef __LP64__
#undef _LP64
#define __ILP32__ 1
#define _ILP32 1
#undef __SIZEOF_LONG__
#define __SIZEOF_LONG__ 4
#undef __SIZEOF_POINTER__
#define __SIZEOF_POINTER__ 4
#undef __SIZEOF_SIZE_T__
#define __SIZEOF_SIZE_T__ 4
#undef __SIZEOF_PTRDIFF_T__
#define __SIZEOF_PTRDIFF_T__ 4
#undef __LONG_MAX__
#define __LONG_MAX__ 0x7fffffffL
#undef __SIZE_MAX__
#define __SIZE_MAX__ 0xffffffffUL
#undef __PTRDIFF_MAX__
#define __PTRDIFF_MAX__ 0x7fffffffL
#undef __INTMAX_MAX__
#define __INTMAX_MAX__ 0x7fffffffffffffffLL
#undef __UINTMAX_MAX__
#define __UINTMAX_MAX__ 0xffffffffffffffffULL
#undef __INTPTR_MAX__
#define __INTPTR_MAX__ 0x7fffffffL
#undef __UINTPTR_MAX__
#define __UINTPTR_MAX__ 0xffffffffUL
#undef __INTMAX_TYPE__
#define __INTMAX_TYPE__ long long
#undef __UINTMAX_TYPE__
#define __UINTMAX_TYPE__ unsigned long long
#undef __INT_LEAST64_TYPE__
#define __INT_LEAST64_TYPE__ long long
#undef __UINT_LEAST64_TYPE__
#define __UINT_LEAST64_TYPE__ unsigned long long
#undef __INT_FAST16_TYPE__
#define __INT_FAST16_TYPE__ int
#undef __INT_FAST32_TYPE__
#define __INT_FAST32_TYPE__ int
#undef __INT_FAST64_TYPE__
#define __INT_FAST64_TYPE__ long long
#undef __UINT_FAST16_TYPE__
#define __UINT_FAST16_TYPE__ unsigned int
#undef __UINT_FAST32_TYPE__
#define __UINT_FAST32_TYPE__ unsigned int
#undef __UINT_FAST64_TYPE__
#define __UINT_FAST64_TYPE__ unsigned long long
#undef __INT64_TYPE__
#define __INT64_TYPE__ long long
#undef __UINT64_TYPE__
#define __UINT64_TYPE__ unsigned long long
`

// predefinedMacros are the macros cmd/cc predefines for the target.
func predefinedMacros() string {
	m := predefined
	if target.ilp32() {
		m += ilp32Macros
	}
	if fenvHardware() {
		m += fenvMacros
	}
	return m
}

// ilp32Headers are the edits, pairs of old and new text, that make the
// headers' types ILP32: clang's wasm32 for what the compiler defines
// (int64_t and intmax_t are long long; size_t, ptrdiff_t, intptr_t stay
// long), and wasi-libc's and musl's choices on 32 bits for the C
// library's (time_t, clock_t, off_t, blkcnt_t, suseconds_t of 64 bits,
// dev_t, ino_t, nlink_t of 64; int_fast16_t and int_fast32_t are int).
// Structs keep their members' types, so struct stat and struct timespec
// have LP64's offsets (a long followed by padding where LP64 has an
// 8-byte member), which internal/c23 writes alike.
var ilp32Headers = map[string][]string{
	"stddef.h": {
		"typedef long max_align_t;", "typedef struct { long long __ll; long double __ld; } max_align_t;",
	},
	"stdint.h": {
		"typedef long int64_t;", "typedef long long int64_t;",
		"typedef unsigned long uint64_t;", "typedef unsigned long long uint64_t;",
		"typedef long int_least64_t;", "typedef long long int_least64_t;",
		"typedef unsigned long uint_least64_t;", "typedef unsigned long long uint_least64_t;",
		"typedef long int_fast16_t;", "typedef int int_fast16_t;",
		"typedef long int_fast32_t;", "typedef int int_fast32_t;",
		"typedef long int_fast64_t;", "typedef long long int_fast64_t;",
		"typedef unsigned long uint_fast16_t;", "typedef unsigned int uint_fast16_t;",
		"typedef unsigned long uint_fast32_t;", "typedef unsigned int uint_fast32_t;",
		"typedef unsigned long uint_fast64_t;", "typedef unsigned long long uint_fast64_t;",
		"typedef long intmax_t;", "typedef long long intmax_t;",
		"typedef unsigned long uintmax_t;", "typedef unsigned long long uintmax_t;",
		"#define INT64_MIN (-9223372036854775807L-1)", "#define INT64_MIN (-9223372036854775807LL-1)",
		"#define INT64_MAX 9223372036854775807L", "#define INT64_MAX 9223372036854775807LL",
		"#define UINT64_MAX 18446744073709551615UL", "#define UINT64_MAX 18446744073709551615ULL",
		"#define INT_FAST16_WIDTH 64", "#define INT_FAST16_WIDTH 32",
		"#define INT_FAST32_WIDTH 64", "#define INT_FAST32_WIDTH 32",
		"#define UINT_FAST16_WIDTH 64", "#define UINT_FAST16_WIDTH 32",
		"#define UINT_FAST32_WIDTH 64", "#define UINT_FAST32_WIDTH 32",
		"#define INTPTR_WIDTH 64", "#define INTPTR_WIDTH 32",
		"#define UINTPTR_WIDTH 64", "#define UINTPTR_WIDTH 32",
		"#define PTRDIFF_WIDTH 64", "#define PTRDIFF_WIDTH 32",
		"#define SIZE_WIDTH 64", "#define SIZE_WIDTH 32",
		"#define INT_FAST16_MIN INT64_MIN", "#define INT_FAST16_MIN INT32_MIN",
		"#define INT_FAST32_MIN INT64_MIN", "#define INT_FAST32_MIN INT32_MIN",
		"#define INT_FAST16_MAX INT64_MAX", "#define INT_FAST16_MAX INT32_MAX",
		"#define INT_FAST32_MAX INT64_MAX", "#define INT_FAST32_MAX INT32_MAX",
		"#define UINT_FAST16_MAX UINT64_MAX", "#define UINT_FAST16_MAX UINT32_MAX",
		"#define UINT_FAST32_MAX UINT64_MAX", "#define UINT_FAST32_MAX UINT32_MAX",
		"#define INTPTR_MIN INT64_MIN", "#define INTPTR_MIN (-2147483647L-1)",
		"#define INTPTR_MAX INT64_MAX", "#define INTPTR_MAX 2147483647L",
		"#define UINTPTR_MAX UINT64_MAX", "#define UINTPTR_MAX 4294967295UL",
		"#define PTRDIFF_MIN INT64_MIN", "#define PTRDIFF_MIN (-2147483647L-1)",
		"#define PTRDIFF_MAX INT64_MAX", "#define PTRDIFF_MAX 2147483647L",
		"#define SIZE_MAX UINT64_MAX", "#define SIZE_MAX 4294967295UL",
		"#define INT64_C(c) c ## L", "#define INT64_C(c) c ## LL",
		"#define UINT64_C(c) c ## UL", "#define UINT64_C(c) c ## ULL",
		"#define INTMAX_C(c) c ## L", "#define INTMAX_C(c) c ## LL",
		"#define UINTMAX_C(c) c ## UL", "#define UINTMAX_C(c) c ## ULL",
	},
	"limits.h": {
		"#define LONG_WIDTH 64", "#define LONG_WIDTH 32",
		"#define ULONG_WIDTH 64", "#define ULONG_WIDTH 32",
		"#define LONG_MIN (-9223372036854775807L-1)", "#define LONG_MIN (-2147483647L-1)",
		"#define LONG_MAX 9223372036854775807L", "#define LONG_MAX 2147483647L",
		"#define ULONG_MAX 18446744073709551615UL", "#define ULONG_MAX 4294967295UL",
	},
	"time.h": {
		"typedef long time_t;\ntypedef long clock_t;", "typedef long long time_t;\ntypedef long long clock_t;",
	},
	"sys/types.h": {
		"typedef long off_t;", "typedef long long off_t;",
		"typedef unsigned long dev_t;", "typedef unsigned long long dev_t;",
		"typedef unsigned long ino_t;", "typedef unsigned long long ino_t;",
		"typedef unsigned long nlink_t;", "typedef unsigned long long nlink_t;",
		"typedef long blkcnt_t;", "typedef long long blkcnt_t;",
		"typedef long suseconds_t;", "typedef long long suseconds_t;",
		"typedef long time_t;\ntypedef long clock_t;", "typedef long long time_t;\ntypedef long long clock_t;",
	},
}

// ilp32Formats makes inttypes.h's PRI and SCN macros ILP32: "ll" for the
// 64-bit and greatest-width types, none for the fast 16- and 32-bit ones
// (int); intptr_t is still long ("l").
func ilp32Formats(h string) string {
	re := regexp.MustCompile(`(?m)^(#define (?:PRI|SCN)[a-zA-Z](64|LEAST64|FAST64|MAX|FAST16|FAST32)) "l`)
	n := 0
	h = re.ReplaceAllStringFunc(h, func(s string) string {
		n++
		m := re.FindStringSubmatch(s)
		if m[2] == "FAST16" || m[2] == "FAST32" {
			return m[1] + ` "`
		}
		return m[1] + ` "ll`
	})
	if n == 0 {
		panic("cmd/cc: inttypes.h has no formats to make ILP32")
	}
	return h
}

// libcLong32 are the library functions whose Go is another where long is
// 4 bytes: those whose range or results in memory are long's. Every other
// one takes and returns LP64's Go types, which the generated code converts
// to and from (libcConv).
var libcLong32 = map[string]string{
	"strtol":  "Strtol32",
	"strtoul": "Strtoul32",
	"ldiv":    "Ldiv32",
	"llogb":   "Llogb32",
	"wcstol":  "Wcstol32",
	"wcstoul": "Wcstoul32",
	"llogbf":  "Llogbf32",
	"llogbl":  "Llogb32",
}

// libcConv converts v, a value of the C type t, to (in) or from (!in)
// the Go type the library's Go has for it: where long is narrower than
// LP64's, a long is passed to and returned from internal/c23 as LP64's
// (int64, uint64), so that one Go function serves every target.
func libcConv(v string, t *ctype, in bool) string {
	if !target.ilp32() || t.kind != kLong && t.kind != kULong {
		return v
	}
	if in {
		return fmt.Sprintf("%s64(%s)", strings.TrimSuffix(goType(t), "32"), v)
	}
	return fmt.Sprintf("%s(%s)", goType(t), v)
}

// long64 is the type of 64 bits that LP64 code makes long: long long where
// long has 32.
func long64(t *ctype) *ctype {
	switch {
	case !target.ilp32() || t.kind != kLong && t.kind != kULong:
		return t
	case t.kind == kLong:
		return tLLong
	}
	return tULLong
}

// ilp32IntLit is the type of an integer constant of value v where long
// has 32 bits (C23 6.4.4.1), given its LP64 type t: the first of long
// and long long that holds it, among those the suffix allows.
func ilp32IntLit(t *ctype, v uint64, suffix string, decimal bool) *ctype {
	if t != tLong && t != tULong {
		return t
	}
	ll := strings.Contains(suffix, "ll")
	u := t == tULong
	switch {
	case !ll && !u && v <= 0x7fffffff:
		return tLong
	case !ll && (u || !decimal) && v <= 0xffffffff:
		return tULong
	case !u && v <= 0x7fffffffffffffff:
		return tLLong
	}
	return tULLong
}

// libcAdapter is the Go function value for the library function name
// (a call of s): where long is narrower than LP64's and the function
// takes or returns one, a closure that converts as a call does (libcConv).
func (g *gen) libcAdapter(name string, s *symbol) string {
	ct := g.calleeType(s)
	if !target.ilp32() || !isLibcGo(name) || ct == nil {
		return name
	}
	ft := ct.fn
	conv := libcConv("x", ft.ret, false) != "x"
	var params, args []string
	for i, pt := range ft.params {
		p := fmt.Sprintf("a%d", i)
		params = append(params, p)
		args = append(args, libcConv(p, pt, true))
		conv = conv || args[i] != p
	}
	if !conv {
		return name
	}
	if ft.variadic {
		args = append(args, "va...")
	}
	if ft.ret.kind == kStruct {
		args = append([]string{"sret"}, args...)
	}
	call := fmt.Sprintf("%s(%s)", name, strings.Join(args, ", "))
	if ft.ret.kind == kVoid || ft.ret.kind == kStruct {
		return fmt.Sprintf("%s { %s }", g.funcSig(ft, params), call)
	}
	return fmt.Sprintf("%s { return %s }", g.funcSig(ft, params), libcConv(call, ft.ret, false))
}

// addrOff is a constant offset of pointer arithmetic, as the target's
// addresses wrap: an Add's offset is a Go int, of 32 bits on wasm32.
func addrOff(n int64) int64 {
	if target.ptrSize == 4 {
		return int64(int32(n))
	}
	return n
}
