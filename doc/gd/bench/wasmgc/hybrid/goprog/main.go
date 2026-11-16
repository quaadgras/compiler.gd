// The reference program for the wasm GC hybrid spike: see hybrid.wat for the
// same program hand-translated to wasm GC objects.
package main

import (
	"sort"
	"time"
)

type node struct{ left, right *node }

func bottomUp(d int) *node {
	if d == 0 {
		return &node{}
	}
	return &node{bottomUp(d - 1), bottomUp(d - 1)}
}

func (n *node) check() int {
	if n.left == nil {
		return 1
	}
	return 1 + n.left.check() + n.right.check()
}

func trees(max int) int {
	sum := bottomUp(max + 1).check()
	long := bottomUp(max)
	for d := 4; d <= max; d += 2 {
		iters := 1 << (max - d + 4)
		for i := 0; i < iters; i++ {
			sum += bottomUp(d).check()
		}
	}
	return sum + long.check()
}

type Vec3 struct{ X, Y, Z float32 }

type Entity struct {
	Pos, Vel Vec3
	Health   int32
	Target   *Entity
}

type Event struct {
	E    *Entity
	Kind int32
}

const N = 20000

var (
	ents  []*Entity
	verts []float32
	rng   uint64 = 88172645463325252
)

func next() uint64 {
	rng ^= rng << 13
	rng ^= rng >> 7
	rng ^= rng << 17
	return rng
}

func newEntity(j int, target *Entity) *Entity {
	return &Entity{
		Pos:    Vec3{float32(j % 100), float32(j % 37), float32(j % 11)},
		Vel:    Vec3{0.1, 0.2, 0.3},
		Health: 100,
		Target: target,
	}
}

func setup() {
	ents = make([]*Entity, N)
	verts = make([]float32, N*3)
	for i := range ents {
		ents[i] = newEntity(i, nil)
	}
	for i, e := range ents {
		e.Target = ents[(i*7+1)%N]
	}
}

//go:noinline
func frame(f int) float32 {
	const dt = 0.016
	events := make([]*Event, 0, N/10)
	for i, e := range ents {
		e.Pos.X += e.Vel.X * dt
		e.Pos.Y += e.Vel.Y * dt
		e.Pos.Z += e.Vel.Z * dt
		t := e.Target
		e.Vel.X += (t.Pos.X - e.Pos.X) * 0.001
		e.Vel.Y += (t.Pos.Y - e.Pos.Y) * 0.001
		e.Vel.Z += (t.Pos.Z - e.Pos.Z) * 0.001
		verts[i*3] = e.Pos.X
		verts[i*3+1] = e.Pos.Y
		verts[i*3+2] = e.Pos.Z
		if i%10 == f%10 {
			events = append(events, &Event{e, int32(i)})
		}
	}
	for _, ev := range events {
		ev.E.Health += ev.Kind & 1
	}
	for k := 0; k < N/100; k++ {
		j := int(next() % N)
		t := int(next() % N)
		ents[j] = newEntity(j, ents[t])
	}
	return verts[(f*7)%(N*3)]
}

func main() {
	t0 := time.Now()
	sum := trees(16)
	println("trees: check", sum, "in", time.Since(t0).Milliseconds(), "ms")

	setup()
	const frames = 2000
	times := make([]float64, frames)
	var check float32
	t0 = time.Now()
	for f := 0; f < frames; f++ {
		s := time.Now()
		check += frame(f)
		times[f] = float64(time.Since(s).Nanoseconds()) / 1e6
	}
	total := time.Since(t0)
	var health int64
	for _, e := range ents {
		health += int64(e.Health)
	}
	sort.Float64s(times)
	println("frames: check", check, "health", health, "total", total.Milliseconds(), "ms")
	println("frame ms: median", times[frames/2], "p99", times[frames*99/100], "max", times[frames-1])
}
