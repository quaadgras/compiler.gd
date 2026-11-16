// fs_prog.go is the Go program of fs_test.mjs.

//go:build ignore

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func list(dir string) string {
	var names []string
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err.Error()
	}
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

func read(name string) string {
	b, err := os.ReadFile(name)
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(b))
}

func check(err error) {
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

func main() {
	switch os.Args[1] {
	case "kept": // /data is kept in IndexedDB, where Emscripten wrote
		fmt.Println("go reads:", read("/data/from_emscripten.txt"))
		fmt.Println("go lists /data:", list("/data"), "| /data/sub:", list("/data/sub"))
		check(os.WriteFile("/data/from_go.txt", []byte("written by Go\n"), 0o644))
		check(os.MkdirAll("/data/sub/go", 0o755))
		check(os.WriteFile("/data/sub/go/deep.txt", []byte("deep\n"), 0o644))
		check(os.Remove("/data/sub/x.bin"))
	case "again": // a later run, with new files in memory
		fmt.Println("go reads again:", read("/data/from_go.txt"), "|", read("/data/sub/go/deep.txt"))
		fmt.Println("go lists /data:", list("/data"), "| /data/sub:", list("/data/sub"))
	case "live": // the files are the Emscripten program's
		fmt.Println("go reads live:", read("/tmp/from_c.txt"), "|", read("/data/from_go.txt"))
		check(os.WriteFile("/tmp/go_live.txt", []byte("written by Go, live\n"), 0o644))
		check(os.Rename("/data/from_go.txt", "/data/renamed.txt"))
		fmt.Println("go lists /data:", list("/data"))
	}
}
