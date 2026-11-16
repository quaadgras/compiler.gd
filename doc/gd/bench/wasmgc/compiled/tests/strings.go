package main

type named struct {
	name string
	n    int
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

//go:noinline
func join(a []string, sep string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += sep
		}
		s += x
	}
	return s
}

func main() {
	a := "hello"
	b := "a considerably longer string, world"
	c := a + ", " + b
	println(c, len(c), c[7], c[len(c)-1])
	println(c[7:19], c[:5] == a, a < b, b < a, a == "hello", c != a)
	words := []string{"alpha", "beta", itoa(12345), "the quick brown fox jumps"}
	j := join(words, "-")
	println(j, len(j))
	bs := []byte(j)
	bs[0] = 'A'
	for i := range bs {
		if bs[i] == '-' {
			bs[i] = '_'
		}
	}
	println(string(bs), len(bs))
	runes := 0
	sum := 0
	for _, r := range "héllo, wörld: 世界" {
		runes++
		sum += int(r)
	}
	println("runes", runes, sum, string(rune(0x4e16)), string(rune('x')))
	n := named{name: itoa(42) + "!", n: 7}
	p := &n
	p.name += p.name
	println(p.name, len(p.name), n.name == "42!42!")
	switch j[:5] {
	case "alpha":
		println("switch ok")
	default:
		println("switch bad")
	}
	var empty string
	println(len(empty), empty == "", empty+"x")
}
