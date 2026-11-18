// run

// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gd escape bits: a method that keeps its argument, called through an
// interface whose itab the runtime built (an assertion from any):
// the itab must have the method's escape mask (from the type's
// method masks), not zero, which would keep &buf on the stack.
package main

import (
	"fmt"
	"os"
)

type putter interface{ Put(p *[8]byte) }

type keeper struct{ kept []*[8]byte }

func (k *keeper) Put(p *[8]byte) { k.kept = append(k.kept, p) }

var sink any = &keeper{}

//go:noinline
func fill(w putter, i int) {
	var buf [8]byte
	for j := range buf {
		buf[j] = byte(i)
	}
	w.Put(&buf) // &localvar to a dynamic callee: an escape candidate
}

//go:noinline
func churn(n int) int {
	var a [256]byte
	for i := range a {
		a[i] = 0xEE
	}
	if n > 0 {
		return churn(n-1) + int(a[n])
	}
	return 0
}

func main() {
	w := sink.(putter) // the itab of (*keeper, putter) is built at run time
	for i := 1; i <= 8; i++ {
		fill(w, i)
		churn(4)
	}
	bad := 0
	for i, p := range sink.(*keeper).kept {
		for _, c := range p {
			if c != byte(i+1) {
				bad++
				fmt.Printf("kept[%d] = %v, want all %d\n", i, *p, i+1)
				break
			}
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}
