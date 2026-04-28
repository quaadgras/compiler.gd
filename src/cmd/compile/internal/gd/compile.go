// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"cmp"
	"internal/race"
	"math/rand"
	"slices"
	"sync"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/liveness"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/pgoir"
	"cmd/compile/internal/ssagen"
	"cmd/compile/internal/staticinit"
	"cmd/compile/internal/types"
	"cmd/compile/internal/walk"
	"cmd/internal/obj"
)

// "Portable" code generation.

// compilequeue lives on Invocation (gd.GdCompileQueue) so concurrent
// compile invocations don't share the queue.

func compilequeue(gd *base.Invocation) []*ir.Func {
	q, _ := gd.GdCompileQueue.([]*ir.Func)
	return q
}

func enqueueFunc(gd *base.Invocation, fn *ir.Func, symABIs *ssagen.SymABIs) {
	if ir.CurFunc(gd) != nil {
		gd.FatalfAt(fn.Pos(), "enqueueFunc %v inside %v", fn, ir.CurFunc(gd))
	}

	if ir.FuncName(fn) == "_" {
		// Skip compiling blank functions.
		// Frontend already reported any spec-mandated errors (#29870).
		return
	}

	if fn.IsClosure() {
		return // we'll get this as part of its enclosing function
	}

	if ssagen.CreateWasmImportWrapper(gd, fn) {
		return
	}

	if len(fn.Body) == 0 {
		if ir.IsIntrinsicSym(fn.Sym()) && fn.Sym().Linkname == "" && !symABIs.HasDef(fn.Sym()) {
			// Generate the function body for a bodyless intrinsic, in case it
			// is used in a non-call context (e.g. as a function pointer).
			// We skip functions defined in assembly, or has a linkname (which
			// could be defined in another package).
			ssagen.GenIntrinsicBody(gd, fn)
		} else {
			// Initialize ABI wrappers if necessary.
			ir.InitLSym(gd, fn, false)
			types.CalcSize(gd, fn.Type())
			a := ssagen.AbiForBodylessFuncStackMap(gd, fn)
			abiInfo := a.ABIAnalyzeFuncType(fn.Type()) // abiInfo has spill/home locations for wrapper
			if fn.ABI == obj.ABI0 {
				// The current args_stackmap generation assumes the function
				// is ABI0, and only ABI0 assembly function can have a FUNCDATA
				// reference to args_stackmap (see cmd/internal/obj/plist.go:Flushplist).
				// So avoid introducing an args_stackmap if the func is not ABI0.
				liveness.WriteFuncMap(gd, fn, abiInfo)

				x := ssagen.EmitArgInfo(gd, fn, abiInfo)
				objw.Global(gd, x, int32(len(x.P)), obj.RODATA|obj.LOCAL)
			}
			return
		}
	}

	errorsBefore := gd.Errors()

	todo := []*ir.Func{fn}
	for len(todo) > 0 {
		next := todo[len(todo)-1]
		todo = todo[:len(todo)-1]

		prepareFunc(gd, next)
		todo = append(todo, next.Closures...)
	}

	if gd.Errors() > errorsBefore {
		return
	}

	// Enqueue just fn itself. compileFunctions will handle
	// scheduling compilation of its closures after it's done.
	gd.GdCompileQueue = append(compilequeue(gd), fn)
}

// prepareFunc handles any remaining frontend compilation tasks that
// aren't yet safe to perform concurrently.
func prepareFunc(gd *base.Invocation, fn *ir.Func) {
	// Set up the function's LSym early to avoid data races with the assemblers.
	// Do this before walk, as walk needs the LSym to set attributes/relocations
	// (e.g. in MarkTypeUsedInInterface).
	ir.InitLSym(gd, fn, true)

	// If this function is a compiler-generated outlined global map
	// initializer function, register its LSym for later processing.
	if m2v := staticinit.MapInitToVar(gd); m2v != nil {
		if _, ok := m2v[fn]; ok {
			ssagen.RegisterMapInitLsym(gd, fn.Linksym())
		}
	}

	// Calculate parameter offsets.
	types.CalcSize(gd, fn.Type())

	// Generate wrappers between Go ABI and Wasm ABI, for a wasmexport
	// function.
	// Must be done after InitLSym and CalcSize.
	ssagen.GenWasmExportWrapper(gd, fn)

	gd.CurFunc = fn
	walk.Walk(gd, fn)
	gd.CurFunc = nil // enforce no further uses of CurFunc

	gd.Ctxt.DwTextCount++
}

// compileFunctions compiles all functions in compilequeue.
// It fans out nBackendWorkers to do the work
// and waits for them to complete.
func compileFunctions(gd *base.Invocation, profile *pgoir.Profile) {
	cq := compilequeue(gd)
	if race.Enabled {
		// Randomize compilation order to try to shake out races.
		tmp := make([]*ir.Func, len(cq))
		perm := rand.Perm(len(cq))
		for i, v := range perm {
			tmp[v] = cq[i]
		}
		copy(cq, tmp)
	} else {
		// Compile the longest functions first,
		// since they're most likely to be the slowest.
		// This helps avoid stragglers.
		slices.SortFunc(cq, func(a, b *ir.Func) int {
			return cmp.Compare(len(b.Body), len(a.Body))
		})
	}

	// By default, we perform work right away on the current goroutine
	// as the solo worker.
	queue := func(work func(int)) {
		work(0)
	}

	if nWorkers := gd.Flag.LowerC; nWorkers > 1 {
		// For concurrent builds, we allow the work queue
		// to grow arbitrarily large, but only nWorkers work items
		// can be running concurrently.
		workq := make(chan func(int))
		done := make(chan int)
		// Dispatcher exits once workq is closed AND every dispatched
		// worker has reported back via done. Without the close-and-
		// drain protocol the loop ran forever, leaving one dispatcher
		// goroutine per in-process compile invocation pinned in memory
		// along with all the func closures it had ever appended to
		// pending — observed as 333 stuck goroutines holding ~3 GB.
		go func() {
			ids := make([]int, nWorkers)
			for i := range ids {
				ids[i] = i
			}
			var pending []func(int)
			active := 0
			for {
				select {
				case work, ok := <-workq:
					if !ok {
						workq = nil
						break
					}
					pending = append(pending, work)
				case id := <-done:
					ids = append(ids, id)
					active--
				}
				if workq == nil && len(pending) == 0 && active == 0 {
					return
				}
				for len(pending) > 0 && len(ids) > 0 {
					work := pending[len(pending)-1]
					id := ids[len(ids)-1]
					pending = pending[:len(pending)-1]
					ids = ids[:len(ids)-1]
					active++
					go func() {
						// Always signal `done`, even if `work` panics
						// or calls runtime.Goexit (e.g. via gd.Fatalf
						// → gd.ErrorExit → gd.Exit). Without this the
						// dispatcher deadlocks waiting for a value
						// that never arrives, masking the real compile
						// error that triggered the abort.
						defer func() { done <- id }()
						work(id)
					}()
				}
			}
		}()
		queue = func(work func(int)) {
			workq <- work
		}
		defer close(workq)
	}

	var wg sync.WaitGroup
	var compile func([]*ir.Func)
	compile = func(fns []*ir.Func) {
		wg.Add(len(fns))
		for _, fn := range fns {
			fn := fn
			queue(func(worker int) {
				// Always call wg.Done, even when ssagen.Compile
				// panics or runtime.Goexit's (e.g. via gd.Fatalf →
				// gd.ErrorExit → gd.Exit). Without this defer,
				// wg.Wait hangs forever and we never get to see the
				// real error. Translate a panic that isn't a known
				// compiler abort into gd.Fatalf so the standard
				// error-reporting path runs.
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						gd.Fatalf("panic during compile of %v: %v", fn, r)
					}
				}()
				ssagen.Compile(gd, fn, worker, profile)
				compile(fn.Closures)
			})
		}
	}

	gd.CalcSizeDisabled = true // not safe to calculate sizes concurrently
	gd.Ctxt.InParallel = true

	compile(cq)
	gd.GdCompileQueue = nil
	wg.Wait()

	gd.Ctxt.InParallel = false
	gd.CalcSizeDisabled = false
}
