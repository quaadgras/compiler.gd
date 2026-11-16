package main

type Shape interface {
	Area() float64
	Name() string
}

type Rect struct{ W, H float64 }
type Circle struct{ R float64 }
type Big struct {
	a, b, c, d float64
	name    string
}

func (r Rect) Area() float64    { return r.W * r.H }
func (r Rect) Name() string     { return "rect" }
func (c *Circle) Area() float64 { return 3 * c.R * c.R }
func (c *Circle) Name() string  { return "circle" }
func (b Big) Area() float64     { return b.a + b.b + b.c + b.d }
func (b Big) Name() string      { return b.name }

type Stringer interface{ String() string }
type ID int

func (i ID) String() string { return "id" }

//go:noinline
func describe(x any) string {
	switch v := x.(type) {
	case nil:
		return "nil"
	case int:
		if v > 5 {
			return "big int"
		}
		return "int"
	case string:
		return "string " + v
	case float64:
		return "float"
	case Shape:
		return "shape " + v.Name()
	case Stringer:
		return "stringer " + v.String()
	case []int:
		return "ints"
	case error:
		return "error " + v.Error()
	}
	return "other"
}

type myErr struct{ msg string }

func (e *myErr) Error() string { return e.msg }

//go:noinline
func total(ss []Shape) (t float64) {
	for _, s := range ss {
		t += s.Area()
	}
	return
}

func main() {
	shapes := []Shape{Rect{2, 3}, &Circle{2}, Big{1, 2, 3, 4, "a big one with a long name"}}
	println(total(shapes), shapes[0].Name(), shapes[1].Name(), shapes[2].Name())
	println(describe(nil), describe(3), describe(7), describe("hey"), describe(1.5))
	println(describe(Rect{1, 1}), describe(ID(4)), describe([]int{1}), describe(&myErr{"oops"}), describe(struct{}{}))
	var a, b any = 5, 5
	var c any = "five"
	var s1, s2 Shape = Rect{1, 2}, Rect{1, 2}
	println(a == b, a == c, s1 == s2, s1 == shapes[0], a != nil)
	r, ok := s1.(Rect)
	_, ok2 := s1.(*Circle)
	st, ok3 := a.(Stringer)
	println(r.W, ok, ok2, ok3, st == nil)
	var e error = &myErr{"wrapped"}
	if me, ok := e.(*myErr); ok {
		println(me.msg, e.Error())
	}
	n := a.(int) + b.(int)
	println(n)
}
