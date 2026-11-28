// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ssagen"
	"cmd/internal/bio"
)

// Units (doc/gd/units.md).
//
// A file with the line "//go:unit" before its package clause, first or
// after one line of comment, is compiled by itself,
// as a C file is: it declares every type it uses and every function and
// variable of the package's other files that it names (a function
// without a body, a variable without a value), and nothing of it is
// inlined into another file but what that file defines too
// (//go:dupok). cmd/cc writes such files, one for each C file.
//
// What is gained is that the object of a unit is a function of that
// file alone, so it is kept: a build compiles the units that changed,
// several at a time, and takes the others' objects from the cache. The
// package's other files are compiled together as ever, and the units'
// objects follow theirs in the package's archive, where the linker
// binds what they define and declare by name (-unit).

const unitMarker = "//go:unit\n"

// isUnitFile reports whether the Go file has the unit marker: its
// first line, or its second after a comment (// Code generated ...).
func isUnitFile(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [512]byte
	n, _ := io.ReadFull(f, buf[:])
	head := string(buf[:n])
	if strings.HasPrefix(head, unitMarker) {
		return true
	}
	first, rest, ok := strings.Cut(head, "\n")
	return ok && strings.HasPrefix(first, "//") && strings.HasPrefix(rest, unitMarker)
}

// splitUnits separates the files to compile into the units and the rest.
func splitUnits(files []string) (units, rest []string) {
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && isUnitFile(f) {
			units = append(units, f)
		} else {
			rest = append(rest, f)
		}
	}
	return
}

// unitCacheDir is where the objects of units are kept: with the go
// command's cache, which go clean -cache removes. It is "" if there is
// to be no cache.
func unitCacheDir() string {
	dir := os.Getenv("GOCACHE")
	if dir == "off" {
		return ""
	}
	if dir == "" {
		c, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(c, "go-build")
	}
	return filepath.Join(dir, "gd-units")
}

// unitArgs sorts the compiler's arguments, without its files, into
// those that a unit's compilation is given too, which with the file say
// what its object is (keyed), and those that do not change the object
// (the directory that -trimpath removes, where the imports are).
func unitArgs(gd *base.Invocation, args []string) (keyed, pass []string, importcfg string) {
	type boolFlag interface{ IsBoolFlag() bool }
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			break // the files
		}
		if a == "--" {
			break
		}
		name := strings.TrimLeft(a, "-")
		val, hasVal := "", false
		if j := strings.IndexByte(name, '='); j >= 0 {
			name, val, hasVal = name[:j], name[j+1:], true
		}
		all := []string{a}
		if f := gd.Flagset.Lookup(name); f != nil && !hasVal {
			if b, ok := f.Value.(boolFlag); !ok || !b.IsBoolFlag() {
				if i+1 < len(args) {
					i++
					val = args[i]
					all = append(all, val)
				}
			}
		}
		switch name {
		case "o", "linkobj", "asmhdr", "symabis", "complete", "unit", "c",
			"bench", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "traceprofile",
			"embedcfg", "buildid", "pgoprofile":
			// The package's, or this compilation's own.
		case "importcfg":
			importcfg = val
			pass = append(pass, all...)
		case "trimpath":
			pass = append(pass, all...)
		default:
			keyed = append(keyed, all...)
		}
	}
	return
}

// importsKey is what the import configuration says, as far as an
// object depends on it: each package with the build id of its file.
func importsKey(importcfg string) string {
	if importcfg == "" {
		return ""
	}
	data, err := os.ReadFile(importcfg)
	if err != nil {
		return "unreadable"
	}
	var b strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "packagefile ")
		if !ok {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		path, file, _ := strings.Cut(rest, "=")
		fmt.Fprintf(&b, "packagefile %s %s\n", path, packageID(file))
	}
	return b.String()
}

// packageID is what says which compilation the package file is: its
// build id, which is in the first lines of the archive (the compiler
// is built without cmd/internal/buildid when it is bootstrapped), or
// failing that the file's size and time.
func packageID(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return file
	}
	defer f.Close()
	var buf [1024]byte
	n, _ := io.ReadFull(f, buf[:])
	const mark = "build id \""
	if i := bytes.Index(buf[:n], []byte(mark)); i >= 0 {
		rest := buf[i+len(mark) : n]
		if j := bytes.IndexByte(rest, '"'); j >= 0 {
			return string(rest[:j])
		}
	}
	if st, err := f.Stat(); err == nil {
		return fmt.Sprintf("%s %d %d", file, st.Size(), st.ModTime().UnixNano())
	}
	return file
}

// compilerKey says which compiler this is.
func compilerKey() string {
	exe, err := os.Executable()
	if err != nil {
		return runtime.Version()
	}
	st, err := os.Stat(exe)
	if err != nil {
		return runtime.Version()
	}
	return fmt.Sprintf("%s %s %d %d", runtime.Version(), exe, st.Size(), st.ModTime().UnixNano())
}

// compileUnits compiles the units, or takes their objects from the
// cache, and returns the objects' files in the units' order.
func compileUnits(archInit func(*ssagen.ArchInfo), gd *base.Invocation, args, units []string) []string {
	keyed, pass, importcfg := unitArgs(gd, args)
	common := sha256.New()
	fmt.Fprintf(common, "gd unit 1\n%s\n%s\n%q\n%s", compilerKey(), *gd.Flag.LowerP, keyed, importsKey(importcfg))
	commonSum := common.Sum(nil)

	dir := unitCacheDir()
	if dir == "" {
		tmp, err := os.MkdirTemp("", "gd-units")
		if err != nil {
			gd.Fatalf("units: %v", err)
		}
		dir = tmp
		gd.AtExit(func() { os.RemoveAll(tmp) })
	}

	objs := make([]string, len(units))
	var todo []int
	for i, u := range units {
		src, err := os.ReadFile(u)
		if err != nil {
			gd.Fatalf("units: %v", err)
		}
		h := sha256.New()
		h.Write(commonSum)
		h.Write(src)
		sum := fmt.Sprintf("%x", h.Sum(nil))
		objs[i] = filepath.Join(dir, sum[:2], sum+".o")
		if _, err := os.Stat(objs[i]); err != nil {
			todo = append(todo, i)
		}
	}
	if gd.Debug.Units != 0 {
		fmt.Fprintf(os.Stderr, "units: %d of %d to compile\n", len(todo), len(units))
	}
	if len(todo) == 0 {
		return objs
	}

	// Several at a time, each with a few threads of its own for the
	// functions of a large one.
	procs := runtime.GOMAXPROCS(0)
	par := min(procs, len(todo))
	each := max(1, min(4, procs/par))
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := false
	for _, i := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := os.MkdirAll(filepath.Dir(objs[i]), 0o777); err != nil {
				mu.Lock()
				failed = true
				mu.Unlock()
				fmt.Fprintf(os.Stderr, "units: %v\n", err)
				return
			}
			tmp := fmt.Sprintf("%s.%d.tmp", objs[i], os.Getpid())
			sub := &base.Invocation{InProcess: true, Stderr: gd.Stderr, Stdout: gd.Stdout}
			var a []string
			a = append(a, keyed...)
			a = append(a, pass...)
			a = append(a, "-unit", fmt.Sprintf("-c=%d", each), "-o", tmp, units[i])
			done := make(chan struct{})
			go func() {
				defer close(done)
				Main(archInit, sub, a)
			}()
			<-done
			sub.RunAtExitFuncs()
			if sub.Status != 0 {
				os.Remove(tmp)
				mu.Lock()
				failed = true
				mu.Unlock()
				return
			}
			if err := os.Rename(tmp, objs[i]); err != nil {
				mu.Lock()
				failed = true
				mu.Unlock()
				fmt.Fprintf(os.Stderr, "units: %v\n", err)
			}
		}(i)
	}
	wg.Wait()
	if failed {
		gd.ErrorExit()
	}
	return objs
}

// dumpUnitObj writes a unit's object by itself: what an archive's entry
// for it holds.
func dumpUnitObj(gd *base.Invocation) {
	bout, err := bio.Create(gd.Flag.LowerO)
	if err != nil {
		gd.FlushErrors()
		fmt.Printf("can't create %s: %v\n", gd.Flag.LowerO, err)
		gd.ErrorExit()
	}
	dumpLinkerObj(gd, bout)
	if err := bout.Close(); err != nil {
		gd.FlushErrors()
		fmt.Printf("error while writing to file %s: %v\n", gd.Flag.LowerO, err)
		gd.ErrorExit()
	}
}

// dumpUnitObjs writes the entries of the units' objects, after the
// package's own.
func dumpUnitObjs(gd *base.Invocation, bout *bio.Writer) {
	for i, name := range gd.UnitObjs {
		data, err := os.ReadFile(name)
		if err != nil {
			gd.Fatalf("units: %v", err)
		}
		if !bytes.HasPrefix(data, []byte("go object ")) {
			gd.Fatalf("units: %s is not an object", name)
		}
		start := startArchiveEntry(bout)
		bout.Write(data)
		finishArchiveEntry(bout, start, fmt.Sprintf("u%06d.o", i))
	}
}
