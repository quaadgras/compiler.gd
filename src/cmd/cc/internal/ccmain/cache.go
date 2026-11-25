// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The cache of translations (cc -cache dir). A translation is kept by
// its command line, with everything it read: the files, by what was in
// them, and the files it looked for and did not find (a header in a
// directory searched before the one that has it would be another
// translation). The same command line when all of that is as it was
// gives the same Go, without translating.
//
// An entry is two files in the cache directory, named by the hash of
// the command line: key.go, the Go, and key.inputs, the list.
//
// What is kept is a whole translation, not each translation unit's: a
// unit's Go is not made alone, since the units are linked as trees
// (gen.go's link), so a change to one file translates all of them.

// inputs records what a translation reads. A nil *inputs records
// nothing.
type inputs struct {
	mu      sync.Mutex
	files   map[string][sha256.Size]byte // by path: the hash of what was read
	missing map[string]bool              // paths that could not be read
}

func newInputs() *inputs {
	return &inputs{files: map[string][sha256.Size]byte{}, missing: map[string]bool{}}
}

// readFile is os.ReadFile, recorded.
func (in *inputs) readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if in == nil {
		return b, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if err != nil {
		in.missing[path] = true
		return nil, err
	}
	if _, ok := in.files[path]; !ok {
		in.files[path] = sha256.Sum256(b)
	}
	return b, nil
}

// A ccCache is the cache in a directory, for one command line.
type ccCache struct {
	dir, key string
}

// cacheFor is the cache in dir for the translation that the command
// line args asks for. Its key has in it what the translation depends on
// and reads no file for: the translator itself, the target, and the
// directory that relative names are in.
func cacheFor(dir string, args []string) (*ccCache, error) {
	h := sha256.New()
	fmt.Fprintf(h, "cc cache 1\n")
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	self, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(h, "self %x\n", sha256.Sum256(self))
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(h, "wd %q\n", wd)
	fmt.Fprintf(h, "target %x\n", sha256.Sum256([]byte(predefinedMacros())))
	fmt.Fprintf(h, "debug %q\n", os.Getenv("CCDEBUG"))
	for _, a := range args {
		fmt.Fprintf(h, "arg %q\n", a)
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, err
	}
	return &ccCache{dir, hex.EncodeToString(h.Sum(nil))}, nil
}

func (c *ccCache) goFile() string     { return filepath.Join(c.dir, c.key+".go") }
func (c *ccCache) inputsFile() string { return filepath.Join(c.dir, c.key+".inputs") }

// get returns the Go that the cache has for the command line, if what
// that translation read is as it was.
func (c *ccCache) get() ([]byte, bool) {
	list, err := os.ReadFile(c.inputsFile())
	if err != nil {
		return nil, false
	}
	var want string
	sc := bufio.NewScanner(bytes.NewReader(list))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		kind, rest, _ := strings.Cut(sc.Text(), " ")
		switch kind {
		case "out":
			want = rest
		case "file":
			sum, q, _ := strings.Cut(rest, " ")
			path, err := strconv.Unquote(q)
			if err != nil {
				return nil, false
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, false
			}
			if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != sum {
				return nil, false
			}
		case "missing":
			path, err := strconv.Unquote(rest)
			if err != nil {
				return nil, false
			}
			if _, err := os.ReadFile(path); err == nil {
				return nil, false // it is there now
			}
		default:
			return nil, false
		}
	}
	if sc.Err() != nil || want == "" {
		return nil, false
	}
	gosrc, err := os.ReadFile(c.goFile())
	if err != nil {
		return nil, false
	}
	if got := sha256.Sum256(gosrc); hex.EncodeToString(got[:]) != want {
		return nil, false
	}
	return gosrc, true
}

// put keeps gosrc, the Go of the translation that read in.
func (c *ccCache) put(gosrc []byte, in *inputs) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "out %x\n", sha256.Sum256(gosrc))
	var paths []string
	for path := range in.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		sum := in.files[path]
		fmt.Fprintf(&b, "file %x %q\n", sum[:], path)
	}
	paths = paths[:0]
	for path := range in.missing {
		if _, ok := in.files[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		fmt.Fprintf(&b, "missing %q\n", path)
	}
	// The list last, and each by a rename: an entry that is found is
	// whole.
	os.Remove(c.inputsFile())
	if err := writeRename(c.goFile(), gosrc); err != nil {
		return err
	}
	return writeRename(c.inputsFile(), b.Bytes())
}

func writeRename(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), name)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
