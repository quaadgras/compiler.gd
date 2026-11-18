// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import "strings"

// layout orders basic blocks in f with the goal of minimizing control flow instructions.
// After this phase returns, the order of f.Blocks matters and is the order
// in which those blocks will appear in the assembly output.
func layout(f *Func) {
	f.Blocks = layoutOrder(f)
}

// Register allocation may use a different order which has constraints
// imposed by the linear-scan algorithm.
func layoutRegallocOrder(f *Func) []*Block {
	// remnant of an experiment; perhaps there will be another.
	return f.Blocks
}

// isColdCall reports whether v calls a slow path: one of internal/c23's
// functions whose names end in "Slow", the rest, apart, of a fast path
// cmd/compile inlines (a pointer's load or store that its fast path does
// not cover). Go's own code calls none (gd fork).
func isColdCall(v *Value) bool {
	if !opcodeTable[v.Op].call {
		return false
	}
	a, ok := v.Aux.(*AuxCall)
	if !ok || a.Fn == nil {
		return false
	}
	name, ok := strings.CutPrefix(a.Fn.Name, "internal/c23.")
	return ok && strings.HasSuffix(name, "Slow")
}

// coldEntries returns, by block ID, the blocks that call a slow path
// (isColdCall), and those with no call that only go on to one (an edge
// split before it); nil if there are none.
func coldEntries(f *Func) []bool {
	var cold []bool
	for _, b := range f.Blocks {
		for _, v := range b.Values {
			if isColdCall(v) {
				if cold == nil {
					cold = make([]bool, f.NumBlocks())
				}
				cold[b.ID] = true
				break
			}
		}
	}
	if cold == nil {
		return nil
	}
	for changed := true; changed; {
		changed = false
		for _, b := range f.Blocks {
			if cold[b.ID] || len(b.Succs) != 1 || !cold[b.Succs[0].b.ID] || b == f.Entry {
				continue
			}
			call := false
			for _, v := range b.Values {
				if opcodeTable[v.Op].call {
					call = true
					break
				}
			}
			if !call {
				cold[b.ID] = true
				changed = true
			}
		}
	}
	return cold
}

func layoutOrder(f *Func) []*Block {
	order := make([]*Block, 0, f.NumBlocks())
	scheduled := f.Cache.allocBoolSlice(f.NumBlocks())
	defer f.Cache.freeBoolSlice(scheduled)
	idToBlock := f.Cache.allocBlockSlice(f.NumBlocks())
	defer f.Cache.freeBlockSlice(idToBlock)
	indegree := f.Cache.allocIntSlice(f.NumBlocks())
	defer f.Cache.freeIntSlice(indegree)
	posdegree := f.newSparseSet(f.NumBlocks()) // blocks with positive remaining degree
	defer f.retSparseSet(posdegree)
	// blocks with zero remaining degree. Use slice to simulate a LIFO queue to implement
	// the depth-first topology sorting algorithm.
	var zerodegree []ID
	// LIFO queue. Track the successor blocks of the scheduled block so that when we
	// encounter loops, we choose to schedule the successor block of the most recently
	// scheduled block.
	var succs []ID
	exit := f.newSparseSet(f.NumBlocks()) // exit blocks
	defer f.retSparseSet(exit)

	// Populate idToBlock and find exit blocks.
	for _, b := range f.Blocks {
		idToBlock[b.ID] = b
		if b.Kind == BlockExit {
			exit.add(b.ID)
		}
	}

	// Expand exit to include blocks post-dominated by exit blocks.
	for {
		changed := false
		for _, id := range exit.contents() {
			b := idToBlock[id]
		NextPred:
			for _, pe := range b.Preds {
				p := pe.b
				if exit.contains(p.ID) {
					continue
				}
				for _, s := range p.Succs {
					if !exit.contains(s.b.ID) {
						continue NextPred
					}
				}
				// All Succs are in exit; add p.
				exit.add(p.ID)
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// Find cold blocks: those the entry does not reach but through a call
	// of a slow path (isColdCall), such as the rest of an inlined function
	// that C's accesses through pointers take apart (gd fork). They are
	// scheduled after the others but the exits, each once a predecessor
	// is (register allocation, which visits blocks in this order, needs
	// one before each block): the fast path falls through, without the
	// cold code between, and is the one register allocation sees first at
	// a merge. A function that calls no slow path has none.
	cold := f.newSparseSet(f.NumBlocks())
	defer f.retSparseSet(cold)
	var coldReady []ID // cold blocks with a predecessor scheduled
	if coldEntry := coldEntries(f); coldEntry != nil {
		hot := f.newSparseSet(f.NumBlocks())
		defer f.retSparseSet(hot)
		hot.add(f.Entry.ID)
		stack := []*Block{f.Entry}
		for len(stack) > 0 {
			b := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range b.Succs {
				if c := e.b; !hot.contains(c.ID) && !coldEntry[c.ID] {
					hot.add(c.ID)
					stack = append(stack, c)
				}
			}
		}
		for _, b := range f.Blocks {
			if !hot.contains(b.ID) && !exit.contains(b.ID) {
				cold.add(b.ID)
			}
		}
	}

	// Initialize indegree of each block
	for _, b := range f.Blocks {
		if exit.contains(b.ID) || cold.contains(b.ID) {
			// exit blocks are always scheduled last, cold ones before them
			continue
		}
		indegree[b.ID] = len(b.Preds)
		for _, pe := range b.Preds {
			if cold.contains(pe.b.ID) {
				// Not waited for: the cold block is scheduled at the end.
				indegree[b.ID]--
			}
		}
		if indegree[b.ID] == 0 {
			// Push an element to the tail of the queue.
			zerodegree = append(zerodegree, b.ID)
		} else {
			posdegree.add(b.ID)
		}
	}

	bid := f.Entry.ID
blockloop:
	for {
		// add block to schedule
		b := idToBlock[bid]
		order = append(order, b)
		scheduled[bid] = true
		if len(order) == len(f.Blocks) {
			break
		}

		// Here, the order of traversing the b.Succs affects the direction in which the topological
		// sort advances in depth. Take the following cfg as an example, regardless of other factors.
		//           b1
		//         0/ \1
		//        b2   b3
		// Traverse b.Succs in order, the right child node b3 will be scheduled immediately after
		// b1, traverse b.Succs in reverse order, the left child node b2 will be scheduled
		// immediately after b1. The test results show that reverse traversal performs a little
		// better.
		// Note: You need to consider both layout and register allocation when testing performance.
		for i := len(b.Succs) - 1; i >= 0; i-- {
			c := b.Succs[i].b
			if cold.contains(c.ID) {
				// Scheduled with the cold blocks, now that it has a
				// predecessor scheduled.
				if !scheduled[c.ID] {
					coldReady = append(coldReady, c.ID)
				}
				continue
			}
			indegree[c.ID]--
			if indegree[c.ID] == 0 {
				posdegree.remove(c.ID)
				zerodegree = append(zerodegree, c.ID)
			} else {
				succs = append(succs, c.ID)
			}
		}

		// Pick the next block to schedule
		// Pick among the successor blocks that have not been scheduled yet.

		// Use likely direction if we have it.
		var likely *Block
		switch b.Likely {
		case BranchLikely:
			likely = b.Succs[0].b
		case BranchUnlikely:
			likely = b.Succs[1].b
		}
		if likely != nil && !scheduled[likely.ID] {
			bid = likely.ID
			continue
		}

		// Use degree for now.
		bid = 0
		// TODO: improve this part
		// No successor of the previously scheduled block works.
		// Pick a zero-degree block if we can.
		for len(zerodegree) > 0 {
			// Pop an element from the tail of the queue.
			cid := zerodegree[len(zerodegree)-1]
			zerodegree = zerodegree[:len(zerodegree)-1]
			if !scheduled[cid] {
				bid = cid
				continue blockloop
			}
		}

		// Still nothing, pick the unscheduled successor block encountered most recently.
		for len(succs) > 0 {
			// Pop an element from the tail of the queue.
			cid := succs[len(succs)-1]
			succs = succs[:len(succs)-1]
			if !scheduled[cid] {
				bid = cid
				continue blockloop
			}
		}

		// Still nothing, pick any non-exit block.
		for posdegree.size() > 0 {
			cid := posdegree.pop()
			if !scheduled[cid] {
				bid = cid
				continue blockloop
			}
		}
		// Then the cold blocks, depth first, each once a predecessor is.
		for len(coldReady) > 0 {
			cid := coldReady[len(coldReady)-1]
			coldReady = coldReady[:len(coldReady)-1]
			if !scheduled[cid] {
				bid = cid
				continue blockloop
			}
		}
		// Pick any exit block.
		// TODO: Order these to minimize jump distances?
		for {
			cid := exit.pop()
			if !scheduled[cid] {
				bid = cid
				continue blockloop
			}
		}
	}
	f.laidout = true
	return order
	//f.Blocks = order
}
