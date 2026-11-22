// errorcheck -0 -live

//go:build !goexperiment.cgocheck2 && !goexperiment.sharing

// Copyright 2016 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Issue 15747: liveness analysis was marking heap-escaped params live too much,
// and worse was using the wrong bitmap bits to do so.

package p

var global *[]byte

type Q struct{}

type T struct{ M string }

var b bool

func f1(q *Q, xx []byte) interface{} { // ERROR "live at call to (newobject|mallocgcSmallScanNoHeaderSC[0-9]+): xx$" "live at entry to f1: xx$"
	// xx was copied from the stack to the heap on the previous line:
	// xx was live for the first two prints but then it switched to &xx
	// being live. We should not see plain xx again.
	if b {
		global = &xx
	}
	xx, _, err := f2(xx, 5) // ERROR "live at call to f2: &xx$"
	if err != nil {
		return err
	}
	return nil
}

//go:noinline
func f2(d []byte, n int) (odata, res []byte, e interface{}) { // ERROR "live at entry to f2: d$"
	if n > len(d) {
		// (gd: with 8-byte words a slice is two words, unpacked on
		// entry: what is live is its parts, and a slice is packed
		// again for the results, which may make the header of a huge
		// one: doc/gd/slice16.md.)
		return d, nil, &T{M: "hello"} // ERROR "live at call to (newobject|mallocgcSmallScanNoHeaderSC[0-9]+)+: (d|.autotmp_[0-9]+)"
	}
	res = d[:n]
	odata = d[n:]
	return // ERROR "live at call to slicehdr: .autotmp_[0-9]+"
}
