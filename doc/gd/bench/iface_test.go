// Benchmarks that track allocation behaviour for small values passed
// through an interface. Under the stock compiler and Phase A of the
// gd fat-interface plan, storing an int64 in an any boxes it on the
// heap — one alloc per store. Phase B will make the same store inline
// (zero allocs) because int64 is pointer-free and fits the 16-byte
// inline payload.
//
// Run:
//   go test -bench=. -benchmem ./doc/gd/bench
//
// Expected under Phase A: BenchmarkAnyInt64-NN  NN  NN.N ns/op  16 B/op  1 allocs/op
// Expected under Phase B: BenchmarkAnyInt64-NN  NN  NN.N ns/op   0 B/op  0 allocs/op
package bench

import "testing"

//go:noinline
func boxInt64(v int64) any { return v }

//go:noinline
func unboxInt64(x any) int64 { return x.(int64) }

func BenchmarkAnyInt64Box(b *testing.B) {
	var sink any
	for i := 0; i < b.N; i++ {
		sink = boxInt64(int64(i))
	}
	_ = sink
}

func BenchmarkAnyInt64RoundTrip(b *testing.B) {
	var sink int64
	for i := 0; i < b.N; i++ {
		sink = unboxInt64(boxInt64(int64(i)))
	}
	_ = sink
}

// For comparison: a pointer-sized *int64 is already stored inline by
// stock Go (direct-iface optimisation), so this should always report
// zero allocs.
//
//go:noinline
func boxPtrInt64(p *int64) any { return p }

func BenchmarkAnyPtrInt64(b *testing.B) {
	x := int64(42)
	var sink any
	for i := 0; i < b.N; i++ {
		sink = boxPtrInt64(&x)
	}
	_ = sink
}
