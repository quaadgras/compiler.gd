package main

import "time"

type node struct {
	val  int64
	next *node
}

var sink *node

//go:noinline
func garbage(n int) int64 {
	var sum int64
	for i := 0; i < n; i++ {
		p := &node{val: int64(i)}
		sink = p // escape: a heap allocation each time
		sum += p.val
	}
	return sum
}

//go:noinline
func build(n int) *node {
	var head *node
	for i := 0; i < n; i++ {
		head = &node{val: int64(i), next: head}
	}
	return head
}

func main() {
	const n = 20000000
	best, bestb := time.Hour, time.Hour
	for r := 0; r < 5; r++ {
		t0 := time.Now()
		garbage(n)
		if d := time.Since(t0); d < best {
			best = d
		}
		t0 = time.Now()
		sink = build(1000000)
		if d := time.Since(t0); d < bestb {
			bestb = d
		}
	}
	println("Go runtime allocator: 2e7 short-lived objects", best.Milliseconds(), "ms =", best.Nanoseconds()/n, "ns each; list build 1e6", bestb.Milliseconds(), "ms")
}
