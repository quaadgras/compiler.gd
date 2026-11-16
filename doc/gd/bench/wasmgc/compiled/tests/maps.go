package main

type point struct{ x, y int }

type rec struct {
	name string
	tags []string
	n    int
}

func main() {
	m := map[string]int{"one": 1, "two": 2, "three": 3}
	m["a rather longer key than the others"] = 40
	m["two"] += 20
	delete(m, "one")
	v, ok := m["three"]
	_, ok2 := m["one"]
	println(len(m), m["two"], v, ok, ok2, m["missing"], m["a rather longer key than the others"])

	sq := make(map[int]int)
	for i := 0; i < 1000; i++ {
		sq[i] = i * i
	}
	sum := 0
	for k, v := range sq {
		if k%2 == 0 {
			delete(sq, k)
		}
		sum += v - k*k
	}
	println(len(sq), sq[999], sq[998], sum)

	pts := map[point]string{{1, 2}: "a", {3, 4}: "b"}
	pts[point{1, 2}] += "!"
	println(len(pts), pts[point{1, 2}], pts[point{3, 4}], pts[point{0, 0}] == "")

	recs := map[string]*rec{}
	for _, n := range []string{"x", "y", "z"} {
		recs[n] = &rec{name: n}
	}
	recs["y"].tags = append(recs["y"].tags, "t1", "t2")
	recs["y"].n++
	byVal := map[int]rec{}
	byVal[7] = *recs["y"]
	r := byVal[7]
	println(len(recs), recs["y"].n, len(recs["y"].tags), r.name, r.tags[1], byVal[8].name == "")

	set := map[*rec]bool{}
	set[recs["x"]] = true
	set[recs["z"]] = true
	set[recs["x"]] = true
	println(len(set), set[recs["x"]], set[recs["y"]])

	any := map[interface{}]int{1: 10, "s": 20, 2.5: 30, point{1, 1}: 40}
	println(any[1], any["s"], any[2.5], any[point{1, 1}], any[int8(1)])

	counts := map[byte]int{}
	for _, c := range []byte("hello, world") {
		counts[c]++
	}
	keys := 0
	total := 0
	for _, n := range counts {
		keys++
		total += n
	}
	println(keys, total, counts['l'], counts['z'])
	var nilmap map[string]int
	println(len(nilmap), nilmap["x"])
	clear(m)
	println(len(m))
}
