// Command cfront translates C to Go that keeps C's pointers safe with
// derived capabilities (doc/gd/cfront.md).
//
//	cfront [-pkg name] [-main] -o out.go in.c
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	pkg := flag.String("pkg", "main", "package of the output")
	withMain := flag.Bool("main", false, "make C's main the program's main")
	out := flag.String("o", "", "output file")
	flag.Parse()
	if flag.NArg() != 1 || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: cfront [-pkg name] [-main] -o out.go in.c")
		os.Exit(2)
	}
	in := flag.Arg(0)
	src, err := os.ReadFile(in)
	if err != nil {
		fail(err)
	}
	toks, err := lex(in, string(src))
	if err != nil {
		fail(err)
	}
	u, err := parse(in, toks)
	if err != nil {
		fail(err)
	}
	gosrc, err := generate(in, u, *pkg, *withMain)
	if gosrc != nil {
		if werr := os.WriteFile(*out, gosrc, 0o666); werr != nil {
			fail(werr)
		}
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cfront:", err)
	os.Exit(1)
}
