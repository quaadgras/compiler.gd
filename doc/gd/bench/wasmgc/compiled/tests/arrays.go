package main

type T struct{ n int }

type S struct {
	name string
	a    [3]int16
	p    *T
}

func pair(x, y uint64) [2]uint64 { return [2]uint64{x, y} }

func sum(a [4]float64) float64 { return a[0] + a[1] + a[2] + a[3] }

func ptrs(a [3]*T) (r [3]*T) {
	r[0], r[1], r[2] = a[2], a[1], a[0]
	return
}

func strs(a [2]string) [2]string { return [2]string{a[1] + "!", a[0] + "?"} }

func mod(s S) S {
	s.a[1] += 10
	s.name += s.name
	s.p = &T{s.p.n * 2}
	return s
}

type I interface{ Get() [2]int }

type G struct{ a, b int }

func (g G) Get() [2]int { return [2]int{g.a, g.b} }

func big(a [40]int) (r [40]int) {
	for i := range a {
		r[39-i] = a[i] * 2
	}
	return
}

func main() {
	p := pair(3, 4)
	println(p[0], p[1])
	println(sum([4]float64{1, 2, 3, 4.5}))
	t1, t2, t3 := &T{1}, &T{2}, &T{3}
	r := ptrs([3]*T{t1, t2, t3})
	println(r[0].n, r[1].n, r[2].n)
	s := strs([2]string{"a", "b"})
	println(s[0], s[1])
	m := mod(S{"x", [3]int16{1, 2, 3}, &T{21}})
	println(m.name, m.a[0], m.a[1], m.a[2], m.p.n)
	var i I = G{5, 6}
	g := i.Get()
	println(g[0], g[1])
	f := func(a [2]byte) [2]byte { return [2]byte{a[1], a[0]} }
	b := f([2]byte{'x', 'y'})
	println(string(b[:]))
	var a [40]int
	for i := range a {
		a[i] = i
	}
	a = big(a)
	println(a[0], a[39])
	arr := [][2]uint64{pair(1, 2), pair(5, 6)}
	println(arr[1][0], arr[0][1])
}
