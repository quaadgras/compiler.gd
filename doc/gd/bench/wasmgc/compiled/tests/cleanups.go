// A block freed with a cleanup, taken again, must not run the cleanups
// of another block when it is freed again.
package main

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

var keep [][]byte
var sink []byte

func collect() {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
}

func main() {
	early := 0
	for round := 0; round < 12; round++ {
		// A block with a cleanup, freed.
		func() {
			b := make([]byte, 1000)
			runtime.AddCleanup(&b[0], func(int) {}, 0)
		}()
		collect()
		// Another block with a cleanup, kept: it takes the list
		// that the first had.
		k := make([]byte, 3000)
		ran := false
		runtime.AddCleanup(&k[0], func(r *bool) { *r = true }, &ran)
		keep = append(keep, k)
		// The first block again, with no cleanup, freed.
		for i := 0; i < 4; i++ {
			sink = make([]byte, 1000)
			sink[0] = 1
		}
		sink = nil
		collect()
		if ran {
			early++
		}
	}
	if early != 0 {
		fmt.Println("FAIL: cleanups of live blocks ran:", early, "of 12")
		os.Exit(1)
	}
	fmt.Println("ok")
}
