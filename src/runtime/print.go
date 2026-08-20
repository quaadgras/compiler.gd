// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/strconv"
	"unsafe"
)

// The compiler knows that a print of a value of this type
// should use printhex instead of printuint (decimal).
type hex uint64

// The compiler knows that a print of a value of this type should use
// printquoted instead of printstring.
type quoted string

// bytes returns a []byte view of s. For heap-rep strings it aliases
// s.str directly (no copy); for inline-rep strings (gd Phase C
// SSO) it heap-materialises a 1..15-byte buffer and returns a slice
// pointing at that, so the result is safe to outlive s.
//
// Stock Go's bytes() unconditionally aliased the string's data
// pointer, which was fine because Go strings have a single rep.
// gd Phase C packs short strings into the header itself, where the
// "data pointer" is the address of the header. Aliasing that for
// any caller that outlives bytes()'s frame produces a dangling
// []byte (the original symptom appeared as garbled \U escapes in
// goroutine label tracebacks). The heap-materialise path keeps the
// helper safe for callers like runtime/race.go that store the
// pointer in long-lived state.
//
// //go:nosplit so callers in nosplit contexts (e.g. tracebacks) can
// still use it; the heap path goes through mallocgc which is
// nosplit-safe under the runtime's own stack invariants.
func bytes(s string) []byte {
	sh := (*stringStruct)(unsafe.Pointer(&s))
	if sh.str != nil {
		// heap rep: alias.
		var b []byte
		bp := (*slice)(unsafe.Pointer(&b))
		bp.array = sh.str
		bp.len = int(sh.len & abi.StringLenMask)
		bp.cap = bp.len
		return b
	}
	// inline rep: heap-materialise so the result outlives s.
	n := int(uint64(sh.len) >> abi.StringTagShift)
	if n == 0 {
		return nil
	}
	p := mallocgc(uintptr(n), nil, false)
	memmove(p, unsafe.Pointer(&sh.hash), uintptr(n))
	var b []byte
	bp := (*slice)(unsafe.Pointer(&b))
	bp.array = p
	bp.len = n
	bp.cap = n
	return b
}

var (
	// printBacklog is a circular buffer of messages written with the builtin
	// print* functions, for use in postmortem analysis of core dumps.
	printBacklog      [512]byte
	printBacklogIndex int
)

// recordForPanic maintains a circular buffer of messages written by the
// runtime leading up to a process crash, allowing the messages to be
// extracted from a core dump.
//
// The text written during a process crash (following "panic" or "fatal
// error") is not saved, since the goroutine stacks will generally be readable
// from the runtime data structures in the core file.
func recordForPanic(b []byte) {
	printlock()

	if panicking.Load() == 0 {
		// Not actively crashing: maintain circular buffer of print output.
		for i := 0; i < len(b); {
			n := copy(printBacklog[printBacklogIndex:], b[i:])
			i += n
			printBacklogIndex += n
			printBacklogIndex %= len(printBacklog)
		}
	}

	printunlock()
}

var debuglock mutex

// The compiler emits calls to printlock and printunlock around
// the multiple calls that implement a single Go print or println
// statement. Some of the print helpers (printslice, for example)
// call print recursively. There is also the problem of a crash
// happening during the print routines and needing to acquire
// the print lock to print information about the crash.
// For both these reasons, let a thread acquire the printlock 'recursively'.

func printlock() {
	mp := getg().m
	mp.locks++ // do not reschedule between printlock++ and lock(&debuglock).
	mp.printlock++
	if mp.printlock == 1 {
		lock(&debuglock)
	}
	mp.locks-- // now we know debuglock is held and holding up mp.locks for us.
}

func printunlock() {
	mp := getg().m
	mp.printlock--
	if mp.printlock == 0 {
		unlock(&debuglock)
	}
}

// write to goroutine-local buffer if diverting output,
// or else standard error.
func gwrite(b []byte) {
	if len(b) == 0 {
		return
	}
	recordForPanic(b)
	gp := getg()
	// Don't use the writebuf if gp.m is dying. We want anything
	// written through gwrite to appear in the terminal rather
	// than be written to in some buffer, if we're in a panicking state.
	// Note that we can't just clear writebuf in the gp.m.dying case
	// because a panic isn't allowed to have any write barriers.
	if gp == nil || gp.writebuf == nil || gp.m.dying > 0 {
		writeErr(b)
		return
	}

	n := copy(gp.writebuf[len(gp.writebuf):cap(gp.writebuf)], b)
	gp.writebuf = gp.writebuf[:len(gp.writebuf)+n]
}

func printsp() {
	printstring(" ")
}

func printnl() {
	printstring("\n")
}

func printbool(v bool) {
	if v {
		printstring("true")
	} else {
		printstring("false")
	}
}

// float64 requires 1+17+1+1+1+3 = 24 bytes max (sign+digits+decimal point+e+sign+exponent digits).
const float64Bytes = 24

func printfloat64(v float64) {
	var buf [float64Bytes]byte
	gwrite(strconv.AppendFloat(buf[:0], v, 'g', -1, 64))
}

// float32 requires 1+9+1+1+1+2 = 15 bytes max (sign+digits+decimal point+e+sign+exponent digits).
const float32Bytes = 15

func printfloat32(v float32) {
	var buf [float32Bytes]byte
	gwrite(strconv.AppendFloat(buf[:0], float64(v), 'g', -1, 32))
}

// complex128 requires 24+24+1+1+1 = 51 bytes max (paren+float64+float64+i+paren).
const complex128Bytes = 2*float64Bytes + 3

func printcomplex128(c complex128) {
	var buf [complex128Bytes]byte
	gwrite(strconv.AppendComplex(buf[:0], c, 'g', -1, 128))
}

// complex64 requires 15+15+1+1+1 = 33 bytes max (paren+float32+float32+i+paren).
const complex64Bytes = 2*float32Bytes + 3

func printcomplex64(c complex64) {
	var buf [complex64Bytes]byte
	gwrite(strconv.AppendComplex(buf[:0], complex128(c), 'g', -1, 64))
}

func printuint(v uint64) {
	// Note: Avoiding strconv.AppendUint so that it's clearer
	// that there are no allocations in this routine.
	// cmd/link/internal/ld.TestAbstractOriginSanity
	// sees the append and doesn't realize it doesn't allocate.
	var buf [20]byte
	i := strconv.RuntimeFormatBase10(buf[:], v)
	gwrite(buf[i:])
}

func printint(v int64) {
	// Note: Avoiding strconv.AppendUint so that it's clearer
	// that there are no allocations in this routine.
	// cmd/link/internal/ld.TestAbstractOriginSanity
	// sees the append and doesn't realize it doesn't allocate.
	neg := v < 0
	u := uint64(v)
	if neg {
		u = -u
	}
	var buf [20]byte
	i := strconv.RuntimeFormatBase10(buf[:], u)
	if neg {
		i--
		buf[i] = '-'
	}
	gwrite(buf[i:])
}

var minhexdigits = 0 // protected by printlock

func printhexopts(include0x bool, mindigits int, v uint64) {
	const dig = "0123456789abcdef"
	var buf [100]byte
	i := len(buf)
	for i--; i > 0; i-- {
		buf[i] = dig[v%16]
		if v < 16 && len(buf)-i >= mindigits {
			break
		}
		v /= 16
	}
	if include0x {
		i--
		buf[i] = 'x'
		i--
		buf[i] = '0'
	}
	gwrite(buf[i:])
}

func printhex(v uint64) {
	printhexopts(true, minhexdigits, v)
}

func printquoted(s string) {
	printlock()
	gwrite([]byte(`"`))
	for i, r := range s {
		switch r {
		case '\n':
			gwrite([]byte(`\n`))
			continue
		case '\r':
			gwrite([]byte(`\r`))
			continue
		case '\t':
			gwrite([]byte(`\t`))
			print()
			continue
		case '\\', '"':
			gwrite([]byte{byte('\\'), byte(r)})
			continue
		case runeError:
			// Distinguish errors from a valid encoding of U+FFFD.
			if _, j := decoderune(s, uint(i)); j == uint(i+1) {
				gwrite([]byte{'\\', 'x'}) // gd Phase C: see below
				printhexopts(false, 2, uint64(s[i]))
				continue
			}
			// Fall through to quoting.
		}
		// For now, only allow basic printable ascii through unescaped
		if r >= ' ' && r <= '~' {
			gwrite([]byte{byte(r)})
		} else if r < 127 {
			// gd Phase C: avoid bytes(`\x`) because the 2-byte
			// literal is emitted as an inline-rep SSO string.
			// bytes() becomes uninlined when Phase G extends its
			// deps, so sp.bytes() returns a pointer into bytes()'s
			// own stack frame that dangles past the return.
			gwrite([]byte{'\\', 'x'})
			printhexopts(false, 2, uint64(r))
		} else if r < 0x1_0000 {
			gwrite([]byte{'\\', 'u'})
			printhexopts(false, 4, uint64(r))
		} else {
			gwrite([]byte{'\\', 'U'})
			printhexopts(false, 8, uint64(r))
		}
	}
	gwrite([]byte{byte('"')})
	printunlock()
}

func printpointer(p unsafe.Pointer) {
	printhex(uint64(uintptr(p)))
}
func printuintptr(p uintptr) {
	printhex(uint64(p))
}

// gd Phase G.2.1: the inline budget for runtime's bytes() is 81
// while the inline max is 80 — the extra outBuf param Phase G
// threads through some of bytes's dependencies (stringStructOf
// etc.) just tips it over. Left uninlined, bytes's return aliases
// its OWN stack param for inline-rep strings, producing dangling
// reads at the gwrite call site. Manually inline the body here
// where &s is printstring's param storage, which lives through
// the gwrite call.
//
//go:nosplit
func printstring(s string) {
	sp := stringStructOf(&s)
	n := sp.length()
	var b []byte
	rp := (*slice)(unsafe.Pointer(&b))
	rp.array = sp.bytes()
	rp.len = n
	rp.cap = n
	gwrite(b)
}

func printslice(s []byte) {
	sp := (*slice)(unsafe.Pointer(&s))
	print("[", len(s), "/", cap(s), "]")
	printpointer(sp.array)
}

func printeface(e eface) {
	print("(", e._type, ",", e.data, ")")
}

func printiface(i iface) {
	print("(", i.tab, ",", i.data, ")")
}
