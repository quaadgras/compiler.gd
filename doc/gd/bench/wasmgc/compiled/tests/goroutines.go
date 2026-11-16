package main

func worker(id int, jobs <-chan int, results chan<- int) {
	for j := range jobs {
		results <- j * j
	}
	results <- -id
}

type msg struct {
	name string
	vals []int
}

func producer(c chan msg, n int) {
	for i := 0; i < n; i++ {
		c <- msg{name: "m", vals: []int{i, i * 2}}
	}
	close(c)
}

func fib(c, quit chan int) {
	x, y := 0, 1
	for {
		select {
		case c <- x:
			x, y = y, x+y
		case <-quit:
			return
		}
	}
}

func main() {
	jobs := make(chan int, 4)
	results := make(chan int)
	for w := 1; w <= 3; w++ {
		go worker(w, jobs, results)
	}
	go func() {
		for i := 1; i <= 10; i++ {
			jobs <- i
		}
		close(jobs)
	}()
	sum, done := 0, 0
	for done < 3 {
		if r := <-results; r < 0 {
			done++
		} else {
			sum += r
		}
	}
	println("workers", sum, done)

	c := make(chan msg)
	go producer(c, 5)
	total, n := 0, 0
	for m := range c {
		total += m.vals[1] + len(m.name)
		n++
	}
	_, ok := <-c
	println("structs", n, total, ok)

	fc, quit := make(chan int), make(chan int)
	go func() {
		s := 0
		for i := 0; i < 10; i++ {
			s += <-fc
		}
		println("fib", s)
		quit <- 0
	}()
	fib(fc, quit)

	buf := make(chan string, 2)
	buf <- "a"
	buf <- "b"
	select {
	case buf <- "c":
		println("not reached")
	default:
		println("full", len(buf), cap(buf))
	}
	println(<-buf, <-buf)

	done2 := make(chan bool)
	go func() {
		defer func() {
			println("recovered:", recover().(error).Error())
			done2 <- true
		}()
		var p *msg
		println(p.name)
	}()
	<-done2

	var nilc chan int
	select {
	case <-nilc:
	default:
		println("nil chan not ready")
	}
	<-make(chan int)
}
