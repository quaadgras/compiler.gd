// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/runtime/sys"
	_ "unsafe" // for go:linkname
)

// Defer, panic and recover.
//
// There is no stack for the runtime to walk. A function that defers
// calls passes deferproc and deferreturn the address of its frame on
// the stack in linear memory, which identifies it: deferreturn runs
// the deferred calls of that frame. A panic runs the deferred calls of
// every frame, innermost first. If one of them recovers, gopanic
// throws a WebAssembly exception, which the functions that defer calls
// catch (the compiler wraps them in a try block): the one whose frame
// the deferred call belonged to runs its remaining deferred calls and
// returns to its caller; the others pass the exception on.
//
// recover recovers only when called by the deferred function itself:
// see gorecoverfp.

type _defer struct {
	fn   func()
	sp   uintptr // the frame of the function that deferred the call
	argp uintptr // where the frame of its caller ends
	link *_defer
}

type _panic struct {
	arg       any
	recovered bool
	goexit    bool    // it is Goexit, which recover does not stop
	sp        uintptr // the stack in linear memory when it started
	argp      uintptr // the stack when the deferred call it runs was made
	passed    bool    // reflect is calling the method of a deferred method value: see reflect_passDeferredCall
	link      *_panic
}

// The compiler adds the frame to its calls of deferproc and
// deferreturn.

func deferproc(fn func(), sp, argp uintptr) {
	gp := curg
	gp.deferred = &_defer{fn: fn, sp: sp, argp: argp, link: gp.deferred}
}

func deferreturn(sp uintptr) {
	gp := curg
	for {
		d := gp.deferred
		if d == nil || d.sp != sp {
			return
		}
		gp.deferred = d.link
		saved := gp.deferArgp
		gp.deferArgp = d.argp
		d.fn()
		gp.deferArgp = saved
	}
}

// recoveringFrame returns the frame of the function that is to return
// after a recovered panic.
func recoveringFrame() uintptr { return curg.recoverSP }

// gcthrow throws the exception that unwinds to that function. The
// compiler replaces a call to it by the instruction.
func gcthrow()

// linsp returns the top of the stack in linear memory. The compiler
// replaces a call to it by the instruction.
func linsp() uintptr

// abort stops the program. The compiler replaces a call to it by the
// unreachable instruction.
func abort()

func gopanic(e any) {
	if e == nil && godebugPanicNil() {
		e = new(PanicNilError)
	}
	gp := curg
	p := &_panic{arg: e, sp: linsp(), link: gp.panicking}
	gp.panicking = p
	for {
		d := gp.deferred
		if d == nil {
			break
		}
		gp.deferred = d.link
		p.argp = linsp()
		p.passed = false
		d.fn()
		if p.recovered {
			// This panic is over, and so are the earlier ones that
			// were started by calls the unwinding removes: those
			// further down the stack than the frame returned to.
			for gp.panicking != nil && gp.panicking.sp <= d.sp {
				gp.panicking = gp.panicking.link
			}
			gp.recoverSP = d.sp
			gcthrow()
		}
	}
	printpanics(p)
	printnl()
	printstring("goroutine ")
	printint(int64(gp.id))
	printstring(" [running]:\n")
	printtraceback()
	exit(2)
}

// printtraceback prints the stack of the goroutine, as Go's traceback
// of a panic does, from the host's stack trace.
func printtraceback() {
	var pcs [64]uintptr
	n := Callers(3, pcs[:])
	for _, pc := range pcs[:n] {
		printstring(FuncForPC(pc).Name())
		printstring("(...)\n\t")
		file, line := pcFileLine(pc)
		printstring(file)
		printstring(":")
		printint(int64(line))
		printnl()
	}
}

// gorecoverfp is recover, called by the function whose caller's frame
// begins at fp: it recovers only if that caller is the panic.
func gorecoverfp(fp uintptr) any {
	p := curg.panicking
	if p == nil || p.recovered || p.goexit {
		return nil
	}
	if fp != p.argp {
		return nil
	}
	p.recovered = true
	return p.arg
}

// gorecoverwrap is recover called by a wrapper, the deferred call of
// defer recover(): it recovers if the function that deferred it was
// called by the panic (and is returning).
func gorecoverwrap() any {
	gp := curg
	p := gp.panicking
	if p == nil || p.recovered || p.goexit || gp.deferArgp == 0 || gp.deferArgp != p.argp {
		return nil
	}
	p.recovered = true
	return p.arg
}

func gorecover() any {
	p := curg.panicking
	if p == nil || p.recovered || p.goexit {
		return nil
	}
	p.recovered = true
	return p.arg
}

func printpanics(p *_panic) {
	if p.link != nil {
		printpanics(p.link)
		printstring("\t")
	}
	printstring("panic: ")
	printpanicval(p.arg)
	if p.recovered {
		printstring(" [recovered]")
	}
	printnl()
}

func printpanicval(v any) {
	switch v := v.(type) {
	case nil:
		printstring("nil")
	case error:
		printstring(v.Error())
	case stringer:
		printstring(v.String())
	case bool:
		printbool(v)
	case int:
		printint(int64(v))
	case int8:
		printint(int64(v))
	case int16:
		printint(int64(v))
	case int32:
		printint(int64(v))
	case int64:
		printint(v)
	case uint:
		printuint(uint64(v))
	case uint8:
		printuint(uint64(v))
	case uint16:
		printuint(uint64(v))
	case uint32:
		printuint(uint64(v))
	case uint64:
		printuint(v)
	case uintptr:
		printuint(uint64(v))
	case float32:
		printfloat32(v)
	case float64:
		printfloat64(v)
	case string:
		printstring(v)
	default:
		printstring("(")
		printstring(toRType((*_type)(unsafePointerOf(efaceOf(&v)._type))).string())
		printstring(") ...")
	}
}

type stringer interface {
	String() string
}

// The Error interface identifies a run time error.
type Error interface {
	error

	// RuntimeError is a no-op function but
	// serves to distinguish types that are run time
	// errors from ordinary errors: a type is a
	// run time error if it has a RuntimeError method.
	RuntimeError()
}

// An errorString represents a runtime error described by a single string.
type errorString string

func (e errorString) RuntimeError() {}

func (e errorString) Error() string {
	return "runtime error: " + string(e)
}

// plainError represents a runtime error that is a string without
// the "runtime error: " prefix.
type plainError string

func (e plainError) RuntimeError() {}

func (e plainError) Error() string {
	return string(e)
}

func itoa(v int64) string {
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	u := uint64(v)
	if neg {
		u = -u
	}
	for {
		i--
		buf[i] = byte(u%10 + '0')
		u /= 10
		if u == 0 {
			break
		}
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// fatal reports an error that cannot be recovered from.
func fatal(s string) {
	printstring("fatal error: ")
	printstring(s)
	printnl()
	exit(2)
}

func throw(s string) { fatal(s) }

var divideError = error(errorString("integer divide by zero"))

func panicdivide()             { panic(divideError) }
func panicshift()              { panic(errorString("negative shift amount")) }
func panicmakeslicelen()       { panic(errorString("makeslice: len out of range")) }
func panicmakeslicecap()       { panic(errorString("makeslice: cap out of range")) }
func panicunsafeslicelen()     { panic(errorString("unsafe.Slice: len out of range")) }
func panicunsafeslicenilptr()  { panic(errorString("unsafe.Slice: ptr is nil and len is not zero")) }
func panicunsafestringlen()    { panic(errorString("unsafe.String: len out of range")) }
func panicunsafestringnilptr() { panic(errorString("unsafe.String: ptr is nil and len is not zero")) }
func panicnil()                { panic(errorString("invalid memory address or nil pointer dereference")) }

// panicwrap is called by a wrapper of a value method that is called on
// a nil pointer.
func panicwrap() { panic(plainError("value method called using nil pointer")) }

var overflowError = error(errorString("integer overflow"))

func panicoverflow() { panic(overflowError) }
func panicfloat()    { panic(errorString("floating point error")) }
func panicmem()      { panicnil() }

func panicrangestate(state int) {
	panic(errorString("range function continued iteration after function for loop body returned false"))
}

// Goexit terminates the goroutine that calls it. No other goroutine is
// affected. Goexit runs all deferred calls before terminating the
// goroutine. Because Goexit is not a panic, any recover calls in those
// deferred functions will return nil.
//
// Calling Goexit from the main goroutine terminates that goroutine
// without func main returning. Since func main has not returned,
// the program continues execution of other goroutines.
// If all other goroutines exit, the program crashes.
func Goexit() {
	gp := curg
	p := &_panic{goexit: true, sp: linsp(), link: gp.panicking}
	gp.panicking = p
	for {
		d := gp.deferred
		if d == nil {
			break
		}
		gp.deferred = d.link
		d.fn()
		if p.recovered {
			// A panic of a deferred call was recovered: the
			// unwinding continues as it would for that panic.
			for gp.panicking != nil && gp.panicking.sp <= d.sp {
				gp.panicking = gp.panicking.link
			}
			gp.recoverSP = d.sp
			gcthrow()
		}
	}
	gp.panicking = p.link
	gdone(gp)
	// The goroutine's stack stays suspended for good, and the host
	// lets it go.
	gp.waiting = "goexit"
	park(uint32(gp.id))
	fatal("Goexit resumed")
}

// deferrangefuncgc returns what identifies the frame sp, for the
// deferprocat calls of the body of a range over a function in it. The
// compiler replaces a call to deferrangefunc by one to it.
func deferrangefuncgc(sp uintptr) any { return sp }

// deferprocat defers fn in the frame that frame identifies.
func deferprocat(fn func(), frame any) {
	deferproc(fn, frame.(uintptr), 0)
}

// A PanicNilError happens when code calls panic(nil).
//
// Before Go 1.21, programs that called panic(nil) observed recover
// returning nil. Starting in Go 1.21, programs that call panic(nil)
// observe recover returning a *PanicNilError. Programs can change back
// to the old behavior by setting GODEBUG=panicnil=1.
type PanicNilError struct {
	// This field makes PanicNilError structurally different from
	// any other struct in this package, and the _ makes it different
	// from any struct in other packages too.
	_ [0]*PanicNilError
}

func (*PanicNilError) Error() string { return "panic called with nil argument (see issue 25448)" }
func (*PanicNilError) RuntimeError() {}

// godebugPanicNil reports whether panic(nil) panics with a
// *PanicNilError: unless GODEBUG has panicnil=1.
func godebugPanicNil() bool {
	env := godebugEnv()
	for i := 0; i+10 <= len(env); i++ {
		if env[i:i+10] == "panicnil=1" && (i == 0 || env[i-1] == ',') {
			return false
		}
	}
	return true
}

// A method value that reflect made (Value.Method(i).Interface()) may be
// a deferred call: the method it calls recovers as if the panic had
// called it, though reflect's functions are in between (as in Go's
// reflect.methodValueCall, which is a wrapper).

// reflect_deferredCall reports whether the function whose caller's
// frame ends at sp was called by the panic, as a deferred call.
//
//go:linkname reflect_deferredCall reflect.deferredCall
func reflect_deferredCall(sp uintptr) bool {
	p := curg.panicking
	return p != nil && !p.recovered && !p.goexit && p.argp == sp
}

// reflect_passDeferredCall makes the next function reflect calls (see
// reflect_gcreflectcall) the deferred call of the panic.
//
//go:linkname reflect_passDeferredCall reflect.passDeferredCall
func reflect_passDeferredCall() {
	if p := curg.panicking; p != nil {
		p.passed = true
	}
}

// reflect_deferHere makes the next function its caller calls the
// deferred call of the panic: the function of a deferred function that
// reflect.MakeFunc made.
//
//go:linkname reflect_deferHere reflect.deferHere
//go:noinline
func reflect_deferHere() {
	if p := curg.panicking; p != nil {
		p.argp = sys.GetCallerSP()
	}
}
