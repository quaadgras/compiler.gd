package main

type strErr string

func (e strErr) Error() string { return string(e) }

type cleanup struct{ log []string }

func (c *cleanup) add(s string) { c.log = append(c.log, s) }

func safeDiv(a, b int) (q int, err error) {
	defer func() {
		if r := recover(); r != nil {
			q = -1
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = strErr("recovered")
			}
		}
	}()
	return a / b, nil
}

func order(c *cleanup) (n int) {
	defer c.add("first deferred")
	defer func() { n *= 2 }()
	for i := 0; i < 3; i++ {
		defer c.add("loop")
	}
	return 21
}

func deep(n int, c *cleanup) {
	defer c.add("unwind")
	if n == 0 {
		panic("bottom")
	}
	deep(n-1, c)
}

func catch(c *cleanup) (msg string) {
	defer func() {
		msg = recover().(string) + "!"
	}()
	deep(3, c)
	return "not reached"
}

func index(a []int, i int) (v int, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return a[i], true
}

func rethrow() (s string) {
	defer func() { s = recover().(string) }()
	defer func() {
		recover()
		panic("second")
	}()
	panic("first")
}

func main() {
	q, err := safeDiv(7, 2)
	println(q, err == nil)
	q, err = safeDiv(7, 0)
	println(q, err.Error())
	c := &cleanup{}
	println(order(c), len(c.log), c.log[0], c.log[3])
	println(catch(c), len(c.log))
	v, ok := index([]int{1, 2, 3}, 1)
	v2, ok2 := index([]int{1, 2, 3}, 5)
	println(v, ok, v2, ok2)
	println(rethrow())
	defer println("deferred in main")
	var m []int
	_ = m[3]
	println("not reached")
}
