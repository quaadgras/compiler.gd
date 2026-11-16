package main

var table = makeTable()
var counter int

func makeTable() []int {
	t := make([]int, 5)
	for i := range t {
		t[i] = i * i
	}
	return t
}

func init() { counter = 100 }

type op func(int) int

func apply(f op, x int) int { return f(x) }

func adder(n int) op { return func(x int) int { counter++; return x + n } }

func double(x int) int { return x * 2 }

var ops = []op{double, adder(3)}

func main() {
	println("init", table[4], counter, len(ops))
	f := adder(10)
	println("closure", apply(f, 1), apply(double, 21), apply(ops[1], 1), counter)
	sum := 0
	each := func(a []int, fn func(int)) {
		for _, x := range a {
			fn(x)
		}
	}
	each(table, func(x int) { sum += x })
	println("capture", sum)
}
