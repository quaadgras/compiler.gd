package main

import "fmt"

type P struct {
	X, Y int
	Name string
}

func main() {
	fmt.Println("hello", 42, 3.5, true)
	fmt.Printf("%d %s %v %+v\n", 7, "str", []int{1, 2}, P{1, 2, "p"})
}
