package main

import "time"

type node struct {
	val  int64
	next *node
}

//go:noinline
func sum(head *node, reps int) int64 {
	var s int64
	for r := 0; r < reps; r++ {
		for p := head; p != nil; p = p.next {
			s += p.val
		}
	}
	return s
}

func main() {
	var head *node
	for i := 0; i < 1000000; i++ {
		head = &node{val: int64(i), next: head}
	}
	best := time.Hour
	for r := 0; r < 5; r++ {
		t0 := time.Now()
		sum(head, 50)
		if d := time.Since(t0); d < best {
			best = d
		}
	}
	println("Go list traverse x50:", best.Milliseconds(), "ms")
}
