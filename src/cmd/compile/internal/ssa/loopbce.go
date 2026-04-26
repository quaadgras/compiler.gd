// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/types"
	"fmt"
)

type indVarFlags uint8

const (
	indVarMinExc    indVarFlags = 1 << iota // minimum value is exclusive (default: inclusive)
	indVarMaxInc                            // maximum value is inclusive (default: exclusive)
	indVarCountDown                         // if set the iteration starts at max and count towards min (default: min towards max)
)

type indVar struct {
	ind   *Value // induction variable
	nxt   *Value // the incremented variable
	min   *Value // minimum value, inclusive/exclusive depends on flags
	max   *Value // maximum value, inclusive/exclusive depends on flags
	entry *Block // entry block in the loop.
	flags indVarFlags
	// Invariant: for all blocks strictly dominated by entry:
	//	min <= ind <  max    [if flags == 0]
	//	min <  ind <  max    [if flags == indVarMinExc]
	//	min <= ind <= max    [if flags == indVarMaxInc]
	//	min <  ind <= max    [if flags == indVarMinExc|indVarMaxInc]
}

// parseIndVar checks whether the SSA value passed as argument is a valid induction
// variable, and, if so, extracts:
//   - the minimum bound
//   - the increment value
//   - the "next" value (SSA value that is Phi'd into the induction variable every loop)
//   - the header's edge returning from the body
//
// Currently, we detect induction variables that match (Phi min nxt),
// with nxt being (Add inc ind).
// If it can't parse the induction variable correctly, it returns (nil, nil, nil).
func parseIndVar(ind *Value) (min, inc, nxt *Value, loopReturn Edge) {
	if ind.Op != OpPhi {
		return
	}

	if n := ind.Args[0]; (n.Op == OpAdd64 || n.Op == OpAdd32 || n.Op == OpAdd16 || n.Op == OpAdd8) && (n.Args[0] == ind || n.Args[1] == ind) {
		min, nxt, loopReturn = ind.Args[1], n, ind.Block.Preds[0]
	} else if n := ind.Args[1]; (n.Op == OpAdd64 || n.Op == OpAdd32 || n.Op == OpAdd16 || n.Op == OpAdd8) && (n.Args[0] == ind || n.Args[1] == ind) {
		min, nxt, loopReturn = ind.Args[0], n, ind.Block.Preds[1]
	} else {
		// Not a recognized induction variable.
		return
	}

	if nxt.Args[0] == ind { // nxt = ind + inc
		inc = nxt.Args[1]
	} else if nxt.Args[1] == ind { // nxt = inc + ind
		inc = nxt.Args[0]
	} else {
		panic("unreachable") // one of the cases must be true from the above.
	}

	return
}

// findIndVar finds induction variables in a function.
//
// Look for variables and blocks that satisfy the following
//
//	 loop:
//	   ind = (Phi min nxt),
//	   if ind < max
//	     then goto enter_loop
//	     else goto exit_loop
//
//	   enter_loop:
//		do something
//	      nxt = inc + ind
//		goto loop
//
//	 exit_loop:
func findIndVar(f *Func) []indVar {
	var iv []indVar
	sdom := f.Sdom()

	for _, b := range f.Blocks {
		if b.Kind != BlockIf || len(b.Preds) != 2 {
			continue
		}

		var ind *Value   // induction variable
		var init *Value  // starting value
		var limit *Value // ending value

		// Check that the control if it either ind </<= limit or limit </<= ind.
		// TODO: Handle unsigned comparisons?
		c := b.Controls[0]
		inclusive := false
		switch c.Op {
		case OpLeq64, OpLeq32, OpLeq16, OpLeq8:
			inclusive = true
			fallthrough
		case OpLess64, OpLess32, OpLess16, OpLess8:
			ind, limit = c.Args[0], c.Args[1]
		default:
			continue
		}

		// See if this is really an induction variable
		less := true
		init, inc, nxt, loopReturn := parseIndVar(ind)
		if init == nil {
			// We failed to parse the induction variable. Before punting, we want to check
			// whether the control op was written with the induction variable on the RHS
			// instead of the LHS. This happens for the downwards case, like:
			//     for i := len(n)-1; i >= 0; i--
			init, inc, nxt, loopReturn = parseIndVar(limit)
			if init == nil {
				// No recognized induction variable on either operand
				continue
			}

			// Ok, the arguments were reversed. Swap them, and remember that we're
			// looking at an ind >/>= loop (so the induction must be decrementing).
			ind, limit = limit, ind
			less = false
		}

		if ind.Block != b {
			// TODO: Could be extended to include disjointed loop headers.
			// I don't think this is causing missed optimizations in real world code often.
			// See https://go.dev/issue/63955
			continue
		}

		// Expect the increment to be a nonzero constant.
		if !inc.isGenericIntConst() {
			continue
		}
		step := inc.AuxInt
		if step == 0 {
			continue
		}

		// startBody is the edge that eventually returns to the loop header.
		var startBody Edge
		switch {
		case sdom.IsAncestorEq(b.Succs[0].b, loopReturn.b):
			startBody = b.Succs[0]
		case sdom.IsAncestorEq(b.Succs[1].b, loopReturn.b):
			// if x { goto exit } else { goto entry } is identical to if !x { goto entry } else { goto exit }
			startBody = b.Succs[1]
			less = !less
			inclusive = !inclusive
		default:
			continue
		}

		// Increment sign must match comparison direction.
		// When incrementing, the termination comparison must be ind </<= limit.
		// When decrementing, the termination comparison must be ind >/>= limit.
		// See issue 26116.
		if step > 0 && !less {
			continue
		}
		if step < 0 && less {
			continue
		}

		// Up to now we extracted the induction variable (ind),
		// the increment delta (inc), the temporary sum (nxt),
		// the initial value (init) and the limiting value (limit).
		//
		// We also know that ind has the form (Phi init nxt) where
		// nxt is (Add inc nxt) which means: 1) inc dominates nxt
		// and 2) there is a loop starting at inc and containing nxt.
		//
		// We need to prove that the induction variable is incremented
		// only when it's smaller than the limiting value.
		// Two conditions must happen listed below to accept ind
		// as an induction variable.

		// First condition: loop entry has a single predecessor, which
		// is the header block.  This implies that b.Succs[0] is
		// reached iff ind < limit.
		if len(startBody.b.Preds) != 1 {
			// the other successor must exit the loop.
			continue
		}

		// Second condition: startBody.b dominates nxt so that
		// nxt is computed when inc < limit.
		if !sdom.IsAncestorEq(startBody.b, nxt.Block) {
			// inc+ind can only be reached through the branch that enters the loop.
			continue
		}

		// Check for overflow/underflow. We need to make sure that inc never causes
		// the induction variable to wrap around.
		// We use a function wrapper here for easy return true / return false / keep going logic.
		// This function returns true if the increment will never overflow/underflow.
		ok := func() bool {
			if step > 0 {
				if limit.isGenericIntConst() {
					// Figure out the actual largest value.
					v := limit.AuxInt
					if !inclusive {
						if v == minSignedValue(limit.Type) {
							return false // < minint is never satisfiable.
						}
						v--
					}
					if init.isGenericIntConst() {
						// Use stride to compute a better lower limit.
						if init.AuxInt > v {
							return false
						}
						// TODO(1.27): investigate passing a smaller-magnitude overflow limit to addU
						// for addWillOverflow.
						v = addU(f.Config.gd, init.AuxInt, diff(f.Config.gd, v, init.AuxInt)/uint64(step)*uint64(step))
					}
					if addWillOverflow(f.Config.gd, v, step, maxSignedValue(ind.Type)) {
						return false
					}
					if inclusive && v != limit.AuxInt || !inclusive && v+1 != limit.AuxInt {
						// We know a better limit than the programmer did. Use our limit instead.
						limit = f.constVal(limit.Op, limit.Type, v, true)
						inclusive = true
					}
					return true
				}
				if step == 1 && !inclusive {
					// Can't overflow because maxint is never a possible value.
					return true
				}
				// If the limit is not a constant, check to see if it is a
				// negative offset from a known non-negative value.
				knn, k := findKNN(limit)
				if knn == nil || k < 0 {
					return false
				}
				// limit == (something nonnegative) - k. That subtraction can't underflow, so
				// we can trust it.
				if inclusive {
					// ind <= knn - k cannot overflow if step is at most k
					return step <= k
				}
				// ind < knn - k cannot overflow if step is at most k+1
				return step <= k+1 && k != maxSignedValue(limit.Type)
			} else { // step < 0
				if limit.isGenericIntConst() {
					// Figure out the actual smallest value.
					v := limit.AuxInt
					if !inclusive {
						if v == maxSignedValue(limit.Type) {
							return false // > maxint is never satisfiable.
						}
						v++
					}
					if init.isGenericIntConst() {
						// Use stride to compute a better lower limit.
						if init.AuxInt < v {
							return false
						}
						// TODO(1.27): investigate passing a smaller-magnitude underflow limit to subU
						// for subWillUnderflow.
						v = subU(f.Config.gd, init.AuxInt, diff(f.Config.gd, init.AuxInt, v)/uint64(-step)*uint64(-step))
					}
					if subWillUnderflow(f.Config.gd, v, -step, minSignedValue(ind.Type)) {
						return false
					}
					if inclusive && v != limit.AuxInt || !inclusive && v-1 != limit.AuxInt {
						// We know a better limit than the programmer did. Use our limit instead.
						limit = f.constVal(limit.Op, limit.Type, v, true)
						inclusive = true
					}
					return true
				}
				if step == -1 && !inclusive {
					// Can't underflow because minint is never a possible value.
					return true
				}
			}
			return false

		}

		if ok() {
			flags := indVarFlags(0)
			var min, max *Value
			if step > 0 {
				min = init
				max = limit
				if inclusive {
					flags |= indVarMaxInc
				}
			} else {
				min = limit
				max = init
				flags |= indVarMaxInc
				if !inclusive {
					flags |= indVarMinExc
				}
				flags |= indVarCountDown
				step = -step
			}
			if f.pass.debug >= 1 {
				printIndVar(b, ind, min, max, step, flags)
			}

			iv = append(iv, indVar{
				ind:   ind,
				nxt:   nxt,
				min:   min,
				max:   max,
				entry: startBody.b,
				flags: flags,
			})
			b.Logf("found induction variable %v (inc = %v, min = %v, max = %v)\n", ind, inc, min, max)
		}

		// TODO: other unrolling idioms
		// for i := 0; i < KNN - KNN % k ; i += k
		// for i := 0; i < KNN&^(k-1) ; i += k // k a power of 2
		// for i := 0; i < KNN&(-k) ; i += k // k a power of 2
	}

	return iv
}

// subWillUnderflow checks if x - y underflows the min value.
// y must be positive.
func subWillUnderflow(gd *base.Invocation, x, y int64, min int64) bool {
	if y < 0 {
		gd.Fatalf("expecting positive value")
	}
	return x < min+y
}

// addWillOverflow checks if x + y overflows the max value.
// y must be positive.
func addWillOverflow(gd *base.Invocation, x, y int64, max int64) bool {
	if y < 0 {
		gd.Fatalf("expecting positive value")
	}
	return x > max-y
}

// diff returns x-y as a uint64. Requires x>=y.
func diff(gd *base.Invocation, x, y int64) uint64 {
	if x < y {
		gd.Fatalf("diff %d - %d underflowed", x, y)
	}
	return uint64(x - y)
}

// addU returns x+y. Requires that x+y does not overflow an int64.
func addU(gd *base.Invocation, x int64, y uint64) int64 {
	if y >= 1<<63 {
		if x >= 0 {
			gd.Fatalf("addU overflowed %d + %d", x, y)
		}
		x += 1<<63 - 1
		x += 1
		y -= 1 << 63
	}
	// TODO(1.27): investigate passing a smaller-magnitude overflow limit in here.
	if addWillOverflow(gd, x, int64(y), maxSignedValue(types.Types[types.TINT64])) {
		gd.Fatalf("addU overflowed %d + %d", x, y)
	}
	return x + int64(y)
}

// subU returns x-y. Requires that x-y does not underflow an int64.
func subU(gd *base.Invocation, x int64, y uint64) int64 {
	if y >= 1<<63 {
		if x < 0 {
			gd.Fatalf("subU underflowed %d - %d", x, y)
		}
		x -= 1<<63 - 1
		x -= 1
		y -= 1 << 63
	}
	// TODO(1.27): investigate passing a smaller-magnitude underflow limit in here.
	if subWillUnderflow(gd, x, int64(y), minSignedValue(types.Types[types.TINT64])) {
		gd.Fatalf("subU underflowed %d - %d", x, y)
	}
	return x - int64(y)
}

// if v is known to be x - c, where x is known to be nonnegative and c is a
// constant, return x, c. Otherwise return nil, 0.
func findKNN(v *Value) (*Value, int64) {
	var x, y *Value
	x = v
	switch v.Op {
	case OpSub64, OpSub32, OpSub16, OpSub8:
		x = v.Args[0]
		y = v.Args[1]

	case OpAdd64, OpAdd32, OpAdd16, OpAdd8:
		x = v.Args[0]
		y = v.Args[1]
		if x.isGenericIntConst() {
			x, y = y, x
		}
	}
	switch x.Op {
	case OpSliceLen, OpStringLen, OpSliceCap:
	case OpPhi, OpCondSelect:
		// Recognise the gd small-string tag-decode pattern as a
		// "non-negative length of string" value for induction-var /
		// bounds-check purposes. Crucially we leave x as the phi
		// itself so the SSA value used at the bounds-check site
		// (which reads stringLen through the same helper and CSEs to
		// the same phi) matches the induction variable's limit.
		if unwrapStringDecodedLen(x) == nil {
			return nil, 0
		}
	default:
		return nil, 0
	}
	if y == nil {
		return x, 0
	}
	if !y.isGenericIntConst() {
		return nil, 0
	}
	if v.Op == OpAdd64 || v.Op == OpAdd32 || v.Op == OpAdd16 || v.Op == OpAdd8 {
		return x, -y.AuxInt
	}
	return x, y.AuxInt
}

func printIndVar(b *Block, i, min, max *Value, inc int64, flags indVarFlags) {
	mb1, mb2 := "[", "]"
	if flags&indVarMinExc != 0 {
		mb1 = "("
	}
	if flags&indVarMaxInc == 0 {
		mb2 = ")"
	}

	mlim1, mlim2 := fmt.Sprint(min.AuxInt), fmt.Sprint(max.AuxInt)
	if !min.isGenericIntConst() {
		if b.Func.pass.debug >= 2 {
			mlim1 = fmt.Sprint(min)
		} else {
			mlim1 = "?"
		}
	}
	if !max.isGenericIntConst() {
		if b.Func.pass.debug >= 2 {
			mlim2 = fmt.Sprint(max)
		} else {
			mlim2 = "?"
		}
	}
	extra := ""
	if b.Func.pass.debug >= 2 {
		extra = fmt.Sprintf(" (%s)", i)
	}
	b.Func.Warnl(b.Pos, "Induction variable: limits %v%v,%v%v, increment %d%s", mb1, mlim1, mlim2, mb2, inc, extra)
}

func minSignedValue(t *types.Type) int64 {
	return -1 << (t.Size()*8 - 1)
}

func maxSignedValue(t *types.Type) int64 {
	return 1<<((t.Size()*8)-1) - 1
}

// effectiveLen peels through the gd small-string tag-decoded len
// pattern when present, returning the OpStringLen value that actually
// references the string. For plain OpSliceLen / OpStringLen / OpSliceCap
// values it returns v unchanged. Returns nil if v is none of the above.
func effectiveLen(v *Value) *Value {
	switch v.Op {
	case OpSliceLen, OpStringLen, OpSliceCap:
		return v
	case OpPhi, OpCondSelect:
		return unwrapStringDecodedLen(v)
	}
	return nil
}

// unwrapStringDecodedLen reports whether v is the tag-aware string-length
// pattern that ssagen's stringLen helper emits under the gd small-string
// optimization, and if so returns the underlying OpStringLen value. The
// pattern is a two-arg phi (or, after branchelim, a CondSelect) whose
// arms are (Rsh64Ux64 sl 60) and (And64 sl (1<<60)-1) for the same
// sl = OpStringLen(str). Returning the OpStringLen value lets prove /
// loopbce treat the decoded length as a plain "length of string" op
// for BCE and induction-variable analysis.
func unwrapStringDecodedLen(v *Value) *Value {
	var a0, a1 *Value
	switch {
	case v.Op == OpPhi && len(v.Args) == 2:
		a0, a1 = v.Args[0], v.Args[1]
	case v.Op == OpCondSelect:
		a0, a1 = v.Args[0], v.Args[1]
	default:
		return nil
	}
	var shift, mask *Value
	switch {
	case a0.Op == OpRsh64Ux64 && a1.Op == OpAnd64:
		shift, mask = a0, a1
	case a0.Op == OpAnd64 && a1.Op == OpRsh64Ux64:
		shift, mask = a1, a0
	default:
		return nil
	}
	if shift.Args[1].Op != OpConst64 || shift.Args[1].AuxInt != 60 {
		return nil
	}
	// And64 is commutative, so the OpStringLen operand can land in
	// either Args[0] or Args[1] depending on canonicalization.
	var maskVal, maskConst *Value
	switch {
	case mask.Args[0].Op == OpConst64:
		maskConst, maskVal = mask.Args[0], mask.Args[1]
	case mask.Args[1].Op == OpConst64:
		maskConst, maskVal = mask.Args[1], mask.Args[0]
	default:
		return nil
	}
	if uint64(maskConst.AuxInt) != 1<<60-1 {
		return nil
	}
	sl := shift.Args[0]
	if sl.Op != OpStringLen || sl != maskVal {
		return nil
	}
	return sl
}
