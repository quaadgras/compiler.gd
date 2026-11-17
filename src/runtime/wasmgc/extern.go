// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/goos"
	"unsafe"
)

// The exported API of package runtime, as far as it means something
// here: the engine collects garbage when it wants to, and there is no
// stack to walk.

const (
	GOOS     = goos.GOOS
	GOARCH   = "wasm"
	Compiler = "gc"
)

// GC cannot ask the engine to collect: it lets the other goroutines
// run, as a collection would.
// GC asks the host to collect garbage, from a task of its own (so that
// the weak references this goroutine looked at can go), on its own
// stack. On the host's stack already, it yields.
func GC() {
	if onSysStack {
		Gosched()
		return
	}
	if jssync() {
		// (A call from JavaScript that has not returned: the host
		// cannot be waited for.)
		return
	}
	if poolcleanup != nil {
		poolcleanup()
	}
	for _, p := range boringCaches {
		*(*unsafe.Pointer)(p) = nil
	}
	// (What KeepAlive kept last is not to be kept by that.)
	KeepAlive((*byte)(nil))
	t0 := nanotime()
	jsbusy(1)
	hostgc(0)
	jsbusy(0)
	gcRecord(nanotime()-t0, walltime())
	gcForced++
	// The cleanups the collection queued may run.
	Gosched()
}

// (allocated is the bytes of linear memory allocated since the last
// collection if this one is due for them, 0 if the program asks for
// it: for a host that cannot make the engine collect, only tell it
// what was allocated.)
//
//go:wasmimport gd gc
func hostgc(allocated int32)

// KeepAlive marks its argument as currently reachable.
func KeepAlive(x any) {}

// SetFinalizer does nothing.
//
// TODO: with the host's FinalizationRegistry.
// SetFinalizer does nothing: finalizers never run, which the
// specification allows (a finalizer needs the object, which the host
// has collected when it says so). runtime.AddCleanup works.
func SetFinalizer(obj any, finalizer any) {
	if efaceOf(&obj)._type == 0 {
		panic(plainError("runtime.SetFinalizer: first argument is nil"))
	}
}

func GOMAXPROCS(n int) int { return 1 }
func NumCPU() int          { return 1 }
func NumGoroutine() int    { return ngoroutine }
func Version() string      { return "gd (GOWASM=gc)" }
func GOROOT() string {
	for _, kv := range syscall_runtime_envs() {
		if len(kv) > 7 && kv[:7] == "GOROOT=" {
			return kv[7:]
		}
	}
	return defaultGOROOT
}

var defaultGOROOT string // set by cmd/link
func Breakpoint()        {}
func LockOSThread()      {}
func UnlockOSThread()    {}

// Caller and Callers walk the host's stack trace: a pc is a function
// (as on js/wasm, its index << 16) plus one and, if the program was not
// linked with -s, which of its lines (see pcFileLine).
func Caller(skip int) (pc uintptr, file string, line int, ok bool) {
	var pcs [1]uintptr
	if Callers(skip+2, pcs[:]) == 0 {
		return 0, "", 0, false
	}
	file, line = pcFileLine(pcs[0])
	return pcs[0], file, line, true
}

// pcFileLine returns the file and line of the code at pc, which the
// host knows from the module's section of lines: "?" and 0 without it.
func pcFileLine(pc uintptr) (file string, line int) {
	var buf [512]byte
	r := pcline(uint64(pc), uint64(uintptr(unsafe.Pointer(&buf[0]))), uint32(len(buf)))
	if r == 0 {
		return "?", 0
	}
	return string(buf[:r>>32]), int(uint32(r))
}

//go:wasmimport gd pcline
func pcline(pc, addr uint64, n uint32) uint64

//go:noinline
func Callers(skip int, pc []uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	// (On the stack when it fits: Callers does not allocate, as
	// log/slog's tests check.)
	var small [64]uintptr
	buf := small[:]
	if need := len(pc) + skip + 16; need > len(small) {
		buf = make([]uintptr, need)
	}
	n := int(callers(uint64(uintptr(unsafe.Pointer(&buf[0]))), uint32(len(buf))))
	// Frames above Callers are the host's.
	start := 0
	for i := 0; i < n && i < 8; i++ {
		if FuncForPC(buf[i]).Name() == "runtime.Callers" {
			start = i
			break
		}
	}
	start += skip
	if start >= n {
		return 0
	}
	return copy(pc, buf[start:n])
}

//go:wasmimport gd callers
func callers(addr uint64, n uint32) uint32

// Stack formats the stack of the calling goroutine (only).
func Stack(buf []byte, all bool) int {
	var pcs [64]uintptr
	n := Callers(1, pcs[:])
	b := buf[:0]
	b = append(b, "goroutine "...)
	b = appendInt(b, curg.id)
	b = append(b, " [running]:\n"...)
	for _, pc := range pcs[:n] {
		b = append(b, FuncForPC(pc).Name()...)
		b = append(b, "(...)\n\t"...)
		file, line := pcFileLine(pc)
		b = append(b, file...)
		b = append(b, ':')
		b = appendInt(b, line)
		b = append(b, " +0x0\n"...) // (as Go's: the offset is not known)
	}
	if len(b) > len(buf) {
		b = b[:len(buf)]
	}
	return copy(buf, b)
}

// A Func represents a Go function in the running binary.
type Func struct {
	opaque struct{}
	entry  uintptr
	pc     uintptr // the pc it was found for: a frame of a call inlined has its own name
}

// Frames may be used to get function/file/line information for a slice
// of PC values returned by Callers: here there are none.
type Frames struct {
	callers []uintptr
}

// Frame is the information returned by Frames for each call frame.
type Frame struct {
	PC        uintptr
	Func      *Func
	Function  string
	File      string
	Line      int
	startLine int
	Entry     uintptr
}

func CallersFrames(callers []uintptr) *Frames { return &Frames{callers: callers} }

func (ci *Frames) Next() (frame Frame, more bool) {
	if len(ci.callers) == 0 {
		return Frame{}, false
	}
	pc := ci.callers[0]
	ci.callers = ci.callers[1:]
	f := FuncForPC(pc)
	file, line := pcFileLine(pc)
	return Frame{PC: pc, Func: f, Function: f.Name(), File: file, Line: line, Entry: pc &^ 0xffff}, len(ci.callers) > 0
}

// FuncForPC returns a Func that knows nothing, or nil for 0.
func FuncForPC(pc uintptr) *Func {
	if pc == 0 {
		return nil
	}
	return &Func{entry: pc &^ 0xffff, pc: pc}
}

// Name returns the name of the function, which the host knows from the
// module's name section.
func (f *Func) Name() string {
	if f == nil {
		return ""
	}
	buf := make([]byte, 512)
	pc := f.pc
	if pc == 0 {
		pc = f.entry
	}
	n := funcname(uint64(pc), uint64(uintptr(unsafe.Pointer(&buf[0]))), uint32(len(buf)))
	return string(buf[:n])
}

func (f *Func) Entry() uintptr {
	if f == nil {
		return 0
	}
	return f.entry
}

// funcname writes the name of the function whose code is pc at addr,
// in linear memory, and returns its length.
//
//go:wasmimport gd funcname
func funcname(pc, addr uint64, n uint32) uint32

// FileLine returns the file and line of the code at pc.
func (f *Func) FileLine(pc uintptr) (file string, line int) {
	if file, line = pcFileLine(pc); line == 0 {
		return "", 0
	}
	return file, line
}

func appendInt(b []byte, v int) []byte {
	if v < 0 {
		b = append(b, '-')
		v = -v
	}
	var d [20]byte
	i := len(d)
	for {
		i--
		d[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return append(b, d[i:]...)
}

// A MemStats records statistics about the memory allocator: none are
// known here.
type MemStats struct {
	Alloc         uint64
	TotalAlloc    uint64
	Sys           uint64
	Lookups       uint64
	Mallocs       uint64
	Frees         uint64
	HeapAlloc     uint64
	HeapSys       uint64
	HeapIdle      uint64
	HeapInuse     uint64
	HeapReleased  uint64
	HeapObjects   uint64
	StackInuse    uint64
	StackSys      uint64
	MSpanInuse    uint64
	MSpanSys      uint64
	MCacheInuse   uint64
	MCacheSys     uint64
	BuckHashSys   uint64
	GCSys         uint64
	OtherSys      uint64
	NextGC        uint64
	LastGC        uint64
	PauseTotalNs  uint64
	PauseNs       [256]uint64
	PauseEnd      [256]uint64
	NumGC         uint32
	NumForcedGC   uint32
	GCCPUFraction float64
	EnableGC      bool
	DebugGC       bool
}

// ReadMemStats reports the number of allocations of Go heap memory
// (as testing.AllocsPerRun counts them) and what is known of linear
// memory: its size, the bytes of its blocks in use and allocated in
// all, and how many times the host was let collect. The engine's heap,
// where the objects are, is not measured.
func ReadMemStats(m *MemStats) {
	*m = MemStats{
		Mallocs:    memstatsMallocs,
		Alloc:      uint64(linLive),
		HeapAlloc:  uint64(linLive),
		TotalAlloc: linTotal,
		Sys:        uint64(linsize()),
		HeapSys:    uint64(linsize()),
		HeapInuse:  uint64(linLive),
		NumGC:      linCollections,

		NumForcedGC:  gcForced,
		LastGC:       gcLast,
		PauseTotalNs: gcPauseTotal,
		PauseNs:      gcPauseNs,
		PauseEnd:     gcPauseEnd,
	}
}

// The collections runtime.GC asked the host for: how long each took
// (the "pause": the program waits for it) and when it ended, the last
// 256 of them as in MemStats.
var (
	gcLast       uint64
	gcPauseTotal uint64
	gcPauseNs    [256]uint64
	gcPauseEnd   [256]uint64
	gcForced     uint32 // by runtime.GC
)

func gcRecord(pause, end int64) {
	i := linCollections % uint32(len(gcPauseNs))
	gcPauseNs[i] = uint64(pause)
	gcPauseEnd[i] = uint64(end)
	gcPauseTotal += uint64(pause)
	gcLast = uint64(end)
	linCollections++
}

// memstatsMallocs counts the allocations that Go makes on its heap:
// those of the runtime's and, through countalloc, the objects that
// escape (runtime.newobject, which the compiler makes inline).
var memstatsMallocs uint64

func countalloc() { memstatsMallocs++ }

// Smaller pieces the compiler calls.

func printslice(s []byte) {
	printstring("[")
	printint(int64(len(s)))
	printstring("/")
	printint(int64(cap(s)))
	printstring("]")
	printpointer(unsafe.Pointer(unsafe.SliceData(s)))
}

func printeface(e any) {
	x := efaceOf(&e)
	printstring("(")
	printhex(uint64(x._type))
	printstring(",")
	printpointer(x.data)
	printstring(")")
}

// printiface prints an interface with methods, which the compiler
// passes as it does any interface: the itab as a pointer.
func printiface(tab unsafe.Pointer, data unsafe.Pointer, w0, w1 float64) {
	printstring("(")
	printhex(uint64(uintptr(tab)))
	printstring(",")
	printpointer(data)
	printstring(")")
}

// stringcopy copies the bytes of s to dst, which has room for dstLen.
func stringcopy(dst *byte, dstLen int, s string) int {
	n := len(s)
	if n > dstLen {
		n = dstLen
	}
	for i := 0; i < n; i++ {
		*(*byte)(add(unsafe.Pointer(dst), uintptr(i))) = s[i]
	}
	return n
}

// slicecopy copies elements without pointers.
func slicecopy(toPtr unsafe.Pointer, toLen int, fromPtr unsafe.Pointer, fromLen int, width uintptr) int {
	n := fromLen
	if toLen < n {
		n = toLen
	}
	if n == 0 || width == 0 {
		return n
	}
	memmove(toPtr, fromPtr, uintptr(n)*width)
	return n
}

func strmin(x, y string) string {
	if y < x {
		return y
	}
	return x
}

func strmax(x, y string) string {
	if y > x {
		return y
	}
	return x
}

func fmin32(x, y float32) float32 { return float32(fmin64(float64(x), float64(y))) }
func fmax32(x, y float32) float32 { return float32(fmax64(float64(x), float64(y))) }

func fmin64(x, y float64) float64 {
	switch {
	case x != x:
		return x
	case y != y:
		return y
	case x == y && x == 0:
		if float64bits(x)>>63 != 0 { // -0 < +0
			return x
		}
		return y
	case y < x:
		return y
	}
	return x
}

func fmax64(x, y float64) float64 {
	switch {
	case x != x:
		return x
	case y != y:
		return y
	case x == y && x == 0:
		if float64bits(x)>>63 != 0 {
			return y
		}
		return x
	case y > x:
		return y
	}
	return x
}

// Cleanup is a handle to a cleanup call for a specific object.
type Cleanup struct {
	id int64
}

// AddCleanup attaches a cleanup function to ptr: the host's
// FinalizationRegistry tells the runtime (runcleanup) once the object
// ptr points into is collected, and a goroutine runs the cleanup. A
// pointer to linear memory (data, a stack) is never collected.
func AddCleanup[T, S any](ptr *T, cleanup func(S), arg S) Cleanup {
	if ptr == nil {
		panic(plainError("runtime.AddCleanup: ptr is nil"))
	}
	return Cleanup{addCleanup(unsafe.Pointer(ptr), func() { cleanup(arg) })}
}

// Stop cancels the cleanup call.
func (c Cleanup) Stop() {
	if c.id != 0 {
		delete(cleanups, c.id)
		hostuncleanup(c.id)
	}
}

var (
	cleanups     map[int64]func()
	lastCleanup  int64
	cleanupQueue []func()
	cleanupG     bool // a goroutine runs the queue
)

// cleanupExport makes runcleanup, which the host calls, part of the
// program when cleanups are.
var cleanupExport func(int64)

func addCleanup(p unsafe.Pointer, fn func()) int64 {
	cleanupExport = runcleanup
	var base uintptr
	if trylinaddr(p) != 0 {
		// Linear memory: of a block (whose header says it has
		// cleanups, which linfree runs), or of data or a stack, which
		// is never freed.
		if base = linbase(p); base == 0 {
			return 0
		}
	}
	if cleanups == nil {
		cleanups = make(map[int64]func())
	}
	lastCleanup++
	cleanups[lastCleanup] = fn
	if base != 0 {
		// The block's list of cleanups, by its index in the header.
		h := linload64(base - linheader)
		i := h >> 32
		if i == 0 {
			if n := len(linCleanupsFree); n > 0 {
				i = linCleanupsFree[n-1] + 1
				linCleanupsFree = linCleanupsFree[:n-1]
			} else {
				linCleanups = append(linCleanups, nil)
				i = uint64(len(linCleanups))
			}
			linstore64(base-linheader, h|i<<32)
		}
		linCleanups[i-1] = append(linCleanups[i-1], lastCleanup)
		return lastCleanup
	}
	if splitMode {
		hostcleanup(p, lastCleanup)
	} else {
		hostcleanup(boxobj(p), lastCleanup)
	}
	return lastCleanup
}

// linCleanups are the cleanups of blocks of linear memory: a block's
// header holds the index of its list (see linfree). linCleanupsFree are
// the indexes free for reuse.
var (
	linCleanups     [][]int64
	linCleanupsFree []uint64
)

// queueLinearCleanups queues the cleanups of list i, whose block is
// being freed.
func queueLinearCleanups(i uint64) {
	ids := linCleanups[i]
	linCleanups[i] = nil
	linCleanupsFree = append(linCleanupsFree, i)
	for _, id := range ids {
		queueCleanup(id)
	}
}

// runcleanup is called by the host (exported as "cleanup") when the
// object of cleanup id was collected: a goroutine is to run it.
//
//go:nosplit
func runcleanup(id int64) {
	systemstack(func() { queueCleanup(id) })
}

// queueCleanup queues the cleanup id, whose object was
// collected, for a goroutine to run.
func queueCleanup(id int64) {
	fn := cleanups[id]
	if fn == nil {
		return
	}
	delete(cleanups, id)
	cleanupQueue = append(cleanupQueue, fn)
	if !cleanupG {
		cleanupG = true
		newproc(runCleanups)
	}
}

func runCleanups() {
	for len(cleanupQueue) > 0 {
		fn := cleanupQueue[0]
		cleanupQueue = cleanupQueue[1:]
		fn()
	}
	cleanupG = false
}

//go:wasmimport gd cleanup
func hostcleanup(p unsafe.Pointer, id int64)

//go:wasmimport gd uncleanup
func hostuncleanup(id int64)

// MemProfileRate controls the fraction of memory allocations that are
// recorded: none are.
var MemProfileRate int = 512 * 1024

// SetBlockProfileRate and SetMutexProfileFraction do nothing.
func SetBlockProfileRate(rate int)         {}
func SetMutexProfileFraction(rate int) int { return 0 }
func NumCgoCall() int64                    { return 0 }
