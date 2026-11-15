// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"internal/buildcfg"
	"slices"
)

// WebAssembly has no jumps, only structured control flow: block, loop
// and if, and branches out of them. The wasm backend's general scheme
// gives every basic block a number and wraps the function in a loop
// around a br_table on that number, so a jump is "set the number,
// branch to the loop" (see cmd/internal/obj/wasm). That also lets a
// function be re-entered in the middle, after one of its calls, which
// is how goroutines are switched: the WebAssembly stack is unwound and
// later rewound. But engines optimize such a dispatch loop poorly.
//
// wasmStructure translates the control-flow graph of a function that
// is never re-entered in the middle into structured control flow,
// following Ramsey, "Beyond Relooper" (ICFP 2022): in reverse
// postorder, a loop header becomes a loop, a block with several
// forward predecessors becomes the code that follows a block opened at
// its immediate dominator, and any other block is placed inline where
// its only predecessor branches to it. The result is f.WasmCF, and
// f.Blocks reordered to the order the blocks are emitted in.
//
// A function qualifies if its control-flow graph is reducible and
// none of its calls can resume: on js with JSPI no call does (see
// runtime/jspi_js.go), otherwise only calls that never return, such
// as panics, do not. Without JSPI that excludes functions with defers,
// whose deferred calls return and which a recovered panic re-enters at
// their deferreturn call. With JSPI the function's recovery handler
// calls deferreturn itself (see cmd/internal/obj/wasm), so the block a
// BlockDefer goes to on recovery is kept but never run.

// WasmCF is the structured control flow of a function.
type WasmCF struct {
	// Pre is emitted before the first block.
	Pre []WasmCFOp
	// Post[b.ID] is emitted after block b's values, in place of
	// its jumps.
	Post [][]WasmCFOp
}

// WasmCFOp is one control-flow instruction.
type WasmCFOp struct {
	Kind WasmCFKind
	// ID identifies a Block or Loop, as the target of a Br or BrIf.
	ID int32
	// Target is the ID of the Block or Loop a Br or BrIf branches
	// to: past the end of a Block, to the start of a Loop.
	Target int32
}

type WasmCFKind uint8

const (
	WasmCFBlock   WasmCFKind = iota // block
	WasmCFLoop                      // loop
	WasmCFEnd                       // end of a block, loop or if
	WasmCFIf                        // if the block's control value is true
	WasmCFIfNot                     // if the block's control value is false
	WasmCFIfNever                   // if false: the code is kept but not run
	WasmCFBr                        // br Target
	WasmCFBrIf                      // br Target if the control value is true
	WasmCFBrIfNot                   // br Target if the control value is false
	WasmCFRet                       // return from the function
	WasmCFExit                      // not reached: the block ends in a call that does not return
)

// wasmNoResume reports whether call v never returns to its caller's
// WebAssembly frame after that frame was unwound.
func wasmNoResume(v *Value, jspi bool) bool {
	if jspi {
		return true
	}
	switch v.Op {
	case OpWasmLoweredTailCall, OpWasmLoweredTailCallInter:
		return true
	}
	// A call that ends an exit block does not return.
	b := v.Block
	return b.Kind == BlockExit && b.Controls[0] == v
}

func wasmStructure(f *Func) {
	if f.Config.arch != "wasm" {
		return
	}
	jspi := buildcfg.GOWASM.JSPI && buildcfg.GOOS == "js"

	for _, b := range f.Blocks {
		switch b.Kind {
		case BlockPlain, BlockIf, BlockRet, BlockRetJmp, BlockExit:
		case BlockDefer:
			if !jspi {
				return
			}
		default:
			return
		}
		for _, v := range b.Values {
			switch v.Op {
			case OpWasmLoweredStaticCall, OpWasmLoweredClosureCall, OpWasmLoweredInterCall,
				OpWasmLoweredTailCall, OpWasmLoweredTailCallInter:
				if !wasmNoResume(v, jspi) {
					return
				}
				if call, ok := v.Aux.(*AuxCall); ok && call.Fn != nil && call.Fn.Name == "runtime.deferreturn" && !jspi {
					return
				}
			}
		}
	}
	if f.fe.Func().HasDefer() && !jspi {
		return
	}

	po := f.Postorder()
	if len(po) != len(f.Blocks) {
		return // unreachable blocks
	}
	rpo := make([]int32, f.NumBlocks()) // reverse postorder number
	for i, b := range po {
		rpo[b.ID] = int32(len(po) - 1 - i)
	}
	idom := f.Idom()
	sdom := f.Sdom()

	// Classify blocks, and check that the graph is reducible:
	// every retreating edge must go to a dominator.
	loopHeader := make([]bool, f.NumBlocks())
	forwardPreds := make([]int32, f.NumBlocks())
	for _, b := range f.Blocks {
		for _, e := range b.Succs {
			s := e.Block()
			if rpo[s.ID] <= rpo[b.ID] {
				if !sdom.IsAncestorEq(s, b) {
					return // irreducible
				}
				loopHeader[s.ID] = true
			} else {
				forwardPreds[s.ID]++
			}
		}
	}
	isMerge := func(b *Block) bool { return forwardPreds[b.ID] >= 2 }

	// mergeChildren[b.ID] are the merge blocks b immediately
	// dominates, last in reverse postorder first.
	mergeChildren := make([][]*Block, f.NumBlocks())
	// size[b.ID] is the number of blocks b dominates, itself included.
	size := make([]int32, f.NumBlocks())
	for _, b := range po { // children before their dominator
		size[b.ID]++
		if d := idom[b.ID]; d != nil {
			size[d.ID] += size[b.ID]
			if isMerge(b) {
				mergeChildren[d.ID] = append(mergeChildren[d.ID], b)
			}
		}
	}
	for _, c := range mergeChildren {
		slices.SortFunc(c, func(x, y *Block) int { return int(rpo[y.ID] - rpo[x.ID]) })
	}

	cf := &WasmCF{Post: make([][]WasmCFOp, f.NumBlocks())}
	order := make([]*Block, 0, len(f.Blocks))
	var nextID int32
	// label[b.ID] is the ID of the open Block that b follows, or of
	// the open Loop b heads if a branch to b goes backward.
	blockLabel := make([]int32, f.NumBlocks())
	loopLabel := make([]int32, f.NumBlocks())
	// open are the open blocks, loops and ifs.
	var open []WasmCFOp

	out := &cf.Pre
	emit := func(op WasmCFOp) { *out = append(*out, op) }
	begin := func(kind WasmCFKind) int32 {
		nextID++
		op := WasmCFOp{Kind: kind, ID: nextID}
		emit(op)
		open = append(open, op)
		return nextID
	}
	end := func() {
		op := open[len(open)-1]
		open = open[:len(open)-1]
		// A branch past the end of the block that ends here is not needed.
		if n := len(*out); n > 0 && op.Kind == WasmCFBlock && (*out)[n-1].Kind == WasmCFBr && (*out)[n-1].Target == op.ID {
			*out = (*out)[:n-1]
		}
		emit(WasmCFOp{Kind: WasmCFEnd, ID: op.ID})
	}

	// target returns the label of the branch from src to dst, or 0 if
	// dst is to be placed where src branches to it.
	target := func(src, dst *Block) int32 {
		if rpo[dst.ID] <= rpo[src.ID] {
			return loopLabel[dst.ID]
		}
		if isMerge(dst) {
			return blockLabel[dst.ID]
		}
		return 0
	}

	var doTree func(b *Block)
	branch := func(src, dst *Block) {
		if t := target(src, dst); t != 0 {
			emit(WasmCFOp{Kind: WasmCFBr, Target: t})
			return
		}
		doTree(dst)
	}
	doTree = func(b *Block) {
		if loopHeader[b.ID] {
			loopLabel[b.ID] = begin(WasmCFLoop)
		}
		merges := mergeChildren[b.ID]
		for _, m := range merges {
			blockLabel[m.ID] = begin(WasmCFBlock)
		}

		order = append(order, b)
		out = &cf.Post[b.ID]
		switch b.Kind {
		case BlockPlain:
			branch(b, b.Succs[0].Block())
		case BlockDefer:
			// Succs[1] is where the runtime would resume the
			// function after a recovered panic.
			begin(WasmCFIfNever)
			branch(b, b.Succs[1].Block())
			end()
			branch(b, b.Succs[0].Block())
		case BlockIf:
			yes, no := b.Succs[0].Block(), b.Succs[1].Block()
			ty, tn := target(b, yes), target(b, no)
			switch {
			case ty != 0:
				emit(WasmCFOp{Kind: WasmCFBrIf, Target: ty})
				branch(b, no)
			case tn != 0:
				emit(WasmCFOp{Kind: WasmCFBrIfNot, Target: tn})
				branch(b, yes)
			default:
				// Both successors are placed here. Neither falls
				// through, so nest one in an if and let the other
				// follow it: the unlikely one, or the smaller.
				nestYes := size[yes.ID] <= size[no.ID]
				switch b.Likely {
				case BranchLikely:
					nestYes = false
				case BranchUnlikely:
					nestYes = true
				}
				if nestYes {
					begin(WasmCFIf)
					doTree(yes)
					end()
					doTree(no)
				} else {
					begin(WasmCFIfNot)
					doTree(no)
					end()
					doTree(yes)
				}
			}
		case BlockRet:
			emit(WasmCFOp{Kind: WasmCFRet})
		case BlockExit:
			emit(WasmCFOp{Kind: WasmCFExit})
		case BlockRetJmp:
			// The tail call returned.
		}

		// The merge blocks follow, each after the end of its block.
		for i := len(merges) - 1; i >= 0; i-- {
			end()
			doTree(merges[i])
		}
		if loopHeader[b.ID] {
			end()
		}
	}
	doTree(f.Entry)

	if len(order) != len(f.Blocks) || len(open) != 0 {
		f.Fatalf("wasmStructure: placed %d of %d blocks, %d constructs left open", len(order), len(f.Blocks), len(open))
	}
	copy(f.Blocks, order)
	f.WasmCF = cf
}
