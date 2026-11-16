// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import (
	"internal/profilerecord"
	"unsafe"
)

// Profiling and execution tracing are not available: the profiles are
// empty and tracing reports that it cannot start.

type StackRecord struct {
	Stack0 [32]uintptr
}

func (r *StackRecord) Stack() []uintptr { return nil }

type BlockProfileRecord struct {
	Count  int64
	Cycles int64
	StackRecord
}

func ThreadCreateProfile(p []StackRecord) (n int, ok bool) { return 0, true }
func GoroutineProfile(p []StackRecord) (n int, ok bool)    { return 0, true }
func BlockProfile(p []BlockProfileRecord) (n int, ok bool) { return 0, true }
func MutexProfile(p []BlockProfileRecord) (n int, ok bool) { return 0, true }
func StartTrace() error                                    { return errorString("tracing is not supported") }
func StopTrace()                                           {}
func ReadTrace() (buf []byte)                              { return nil }

//go:linkname pprof_goroutineProfileWithLabels
func pprof_goroutineProfileWithLabels(p []profilerecord.StackRecord, labels []unsafe.Pointer) (n int, ok bool) {
	return 0, true
}

//go:linkname pprof_goroutineLeakProfileWithLabels
func pprof_goroutineLeakProfileWithLabels(p []profilerecord.StackRecord, labels []unsafe.Pointer) (n int, ok bool) {
	return 0, true
}

//go:linkname pprof_memProfileInternal
func pprof_memProfileInternal(p []profilerecord.MemProfileRecord, inuseZero bool) (n int, ok bool) {
	return 0, true
}

//go:linkname pprof_blockProfileInternal
func pprof_blockProfileInternal(p []profilerecord.BlockProfileRecord) (n int, ok bool) {
	return 0, true
}

//go:linkname pprof_mutexProfileInternal
func pprof_mutexProfileInternal(p []profilerecord.BlockProfileRecord) (n int, ok bool) {
	return 0, true
}

//go:linkname pprof_threadCreateInternal
func pprof_threadCreateInternal(p []profilerecord.StackRecord) (n int, ok bool) { return 0, true }

//go:linkname pprof_fpunwindExpand
func pprof_fpunwindExpand(dst, src []uintptr) int { return copy(dst, src) }

//go:linkname pprof_makeProfStack
func pprof_makeProfStack() []uintptr { return make([]uintptr, 32) }

//go:linkname pprof_cyclesPerSecond runtime/pprof.runtime_cyclesPerSecond
func pprof_cyclesPerSecond() int64 { return 1e9 }

// A CPU profile has no samples (nothing interrupts a goroutine to take
// them), but it is well formed: the record of its rate, then the end
// when profiling stops.
var (
	cpuprofRate   int
	cpuprofHeader bool // the record of the rate is to be read
	cpuprofEOF    bool // profiling stopped: the end is to be read
	cpuprofReader *g   // blocked in readProfile
)

// SetCPUProfileRate sets the CPU profiling rate to hz samples per
// second; hz <= 0 turns profiling off.
func SetCPUProfileRate(hz int) {
	if hz < 0 {
		hz = 0
	}
	switch {
	case hz > 0 && cpuprofRate == 0:
		cpuprofRate = hz
		cpuprofHeader = true
		cpuprofEOF = false
	case hz > 0:
		print("runtime: cannot set cpu profile rate until previous profile has finished.\n")
	case cpuprofRate != 0:
		cpuprofRate = 0
		cpuprofEOF = true
		if gp := cpuprofReader; gp != nil {
			cpuprofReader = nil
			goready(gp)
		}
	}
}

//go:linkname runtime_pprof_readProfile runtime/pprof.readProfile
func runtime_pprof_readProfile() ([]uint64, []unsafe.Pointer, bool) {
	if cpuprofHeader {
		cpuprofHeader = false
		return []uint64{3, 0, uint64(cpuprofRate)}, []unsafe.Pointer{nil}, false
	}
	for cpuprofRate != 0 && !cpuprofEOF {
		cpuprofReader = curg
		gopark("profile read")
	}
	cpuprofEOF = false
	return nil, nil, true
}

//go:linkname pprof_FrameStartLine runtime/pprof.runtime_FrameStartLine
func pprof_FrameStartLine(f *Frame) int { return 0 }

//go:linkname pprof_FrameSymbolName runtime/pprof.runtime_FrameSymbolName
func pprof_FrameSymbolName(f *Frame) string { return f.Function }

//go:linkname pprof_expandFinalInlineFrame runtime/pprof.runtime_expandFinalInlineFrame
func pprof_expandFinalInlineFrame(stk []uintptr) []uintptr { return stk }

var profLabel unsafe.Pointer

//go:linkname pprof_setProfLabel runtime/pprof.runtime_setProfLabel
func pprof_setProfLabel(labels unsafe.Pointer) { profLabel = labels }

//go:linkname pprof_getProfLabel runtime/pprof.runtime_getProfLabel
func pprof_getProfLabel() unsafe.Pointer { return profLabel }

//go:linkname pprof_goroutineleakcount runtime/pprof.runtime_goroutineleakcount
func pprof_goroutineleakcount() int { return 0 }

//go:linkname pprof_goroutineLeakGC runtime/pprof.runtime_goroutineLeakGC
func pprof_goroutineLeakGC() {}

//go:linkname traceClockNow
func traceClockNow() uint64 { return uint64(nanotime()) }

//go:linkname traceClockUnitsPerSecond runtime/trace.runtime_traceClockUnitsPerSecond
func traceClockUnitsPerSecond() uint64 { return 1e9 }

//go:linkname traceAdvance
func traceAdvance(stopTrace bool) {}

//go:linkname trace_userTaskCreate runtime/trace.userTaskCreate
func trace_userTaskCreate(id, parentID uint64, taskType string) {}

//go:linkname trace_userTaskEnd runtime/trace.userTaskEnd
func trace_userTaskEnd(id uint64) {}

//go:linkname trace_userRegion runtime/trace.userRegion
func trace_userRegion(id, mode uint64, regionType string) {}

//go:linkname trace_userLog runtime/trace.userLog
func trace_userLog(id uint64, category, message string) {}
