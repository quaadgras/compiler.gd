// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package staticdata

import (
	"path"
	"sort"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
)

const (
	embedUnknown = iota
	embedBytes
	embedString
	embedFiles
)

func embedFileList(gd *base.Invocation, v *ir.Name, kind int) []string {
	// Build list of files to store.
	have := make(map[string]bool)
	var list []string
	for _, e := range *v.Embed {
		for _, pattern := range e.Patterns {
			files, ok := gd.Flag.Cfg.Embed.Patterns[pattern]
			if !ok {
				gd.ErrorfAt(e.Pos, 0, "invalid go:embed: build system did not map pattern: %s", pattern)
			}
			for _, file := range files {
				if gd.Flag.Cfg.Embed.Files[file] == "" {
					gd.ErrorfAt(e.Pos, 0, "invalid go:embed: build system did not map file: %s", file)
					continue
				}
				if !have[file] {
					have[file] = true
					list = append(list, file)
				}
				if kind == embedFiles {
					for dir := path.Dir(file); dir != "." && !have[dir]; dir = path.Dir(dir) {
						have[dir] = true
						list = append(list, dir+"/")
					}
				}
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return embedFileLess(list[i], list[j])
	})

	if kind == embedString || kind == embedBytes {
		if len(list) > 1 {
			gd.ErrorfAt(v.Pos(), 0, "invalid go:embed: multiple files for type %v", v.Type())
			return nil
		}
	}

	return list
}

// embedKind determines the kind of embedding variable.
func embedKind(typ *types.Type) int {
	if typ.Sym() != nil && typ.Sym().Name == "FS" && typ.Sym().Pkg.Path == "embed" {
		return embedFiles
	}
	if typ.Kind() == types.TSTRING {
		return embedString
	}
	if typ.IsSlice() && typ.Elem().Kind() == types.TUINT8 {
		return embedBytes
	}
	return embedUnknown
}

func embedFileNameSplit(name string) (dir, elem string, isDir bool) {
	name, isDir = strings.CutSuffix(name, "/")
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ".", name, isDir
	}
	return name[:i], name[i+1:], isDir
}

// embedFileLess implements the sort order for a list of embedded files.
// See the comment inside ../../../../embed/embed.go's Files struct for rationale.
func embedFileLess(x, y string) bool {
	xdir, xelem, _ := embedFileNameSplit(x)
	ydir, yelem, _ := embedFileNameSplit(y)
	return xdir < ydir || xdir == ydir && xelem < yelem
}

// WriteEmbed emits the init data for a //go:embed variable,
// which is either a string, a []byte, or an embed.FS.
func WriteEmbed(gd *base.Invocation, v *ir.Name) {
	// TODO(mdempsky): User errors should be reported by the frontend.

	commentPos := (*v.Embed)[0].Pos
	if gd.Flag.Cfg.Embed.Patterns == nil {
		gd.ErrorfAt(commentPos, 0, "invalid go:embed: build system did not supply embed configuration")
		return
	}
	kind := embedKind(v.Type())
	if kind == embedUnknown {
		gd.ErrorfAt(v.Pos(), 0, "go:embed cannot apply to var of type %v", v.Type())
		return
	}

	files := embedFileList(gd, v, kind)
	if gd.Errors() > 0 {
		return
	}
	switch kind {
	case embedString, embedBytes:
		file := files[0]
		fsym, size, err := fileStringSym(gd, v.Pos(), gd.Flag.Cfg.Embed.Files[file], kind == embedString, nil)
		if err != nil {
			gd.ErrorfAt(v.Pos(), 0, "embed %s: %v", file, err)
		}
		sym := v.Linksym()
		off := 0
		off = objw.SymPtr(gd, sym, off, fsym, 0) // data string
		if kind == embedString {
			off = stringRest(gd, sym, off, uint64(size))
		} else {
			// embedBytes: the slice's len and cap. No hash slot.
			sliceRest(gd, sym, off, uint64(size))
		}

	case embedFiles:
		slicedata := v.Sym().Pkg.Lookup(v.Sym().Name + `.files`).Linksym(gd)
		off := 0
		// []files pointed at by Files
		// (The files are where the slice ends, which their strings
		// are aligned for: the slice is 16 bytes or 3 words.)
		off = objw.SymPtr(gd, slicedata, off, slicedata, int(types.SliceSize)) // []file, pointing just past slice
		off = sliceRest(gd, slicedata, off, uint64(len(files)))

		// embed/embed.go type file is:
		//	name string
		//	data string
		//	hash [16]byte
		// Emit one of these per file in the set (stringRest writes the
		// words of a string after its pointer).
		const hashSize = 16
		hash := make([]byte, hashSize)
		for _, file := range files {
			// name string
			off = objw.SymPtr(gd, slicedata, off, StringSym(gd, v.Pos(), file), 0)
			off = stringRest(gd, slicedata, off, uint64(len(file)))
			if strings.HasSuffix(file, "/") {
				// entry for directory - no data; zero out the data string.
				off = objw.Uintptr(gd, slicedata, off, 0) // data.ptr
				off = stringRest(gd, slicedata, off, 0)
				off += hashSize
			} else {
				fsym, size, err := fileStringSym(gd, v.Pos(), gd.Flag.Cfg.Embed.Files[file], true, hash)
				if err != nil {
					gd.ErrorfAt(v.Pos(), 0, "embed %s: %v", file, err)
				}
				// data string
				off = objw.SymPtr(gd, slicedata, off, fsym, 0)
				off = stringRest(gd, slicedata, off, uint64(size))
				off = int(slicedata.WriteBytes(gd.Ctxt, int64(off), hash))
			}
		}
		objw.Global(gd, slicedata, int32(off), obj.RODATA|obj.LOCAL)
		sym := v.Linksym()
		objw.SymPtr(gd, sym, 0, slicedata, 0)
	}
}

// sliceRest writes the length and capacity n of a slice after its
// pointer, at off in sym, and returns the offset after them: uint32s
// where a slice is packed (types.SlicePacked), else words.
func sliceRest(gd *base.Invocation, sym *obj.LSym, off int, n uint64) int {
	if types.SlicePacked() {
		if n >= types.SliceHugeCap {
			gd.Fatalf("embedded slice of %d bytes", n)
		}
		off = objw.Uint32(gd, sym, off, uint32(n))
		return objw.Uint32(gd, sym, off, uint32(n))
	}
	off = objw.Uintptr(gd, sym, off, n)
	return objw.Uintptr(gd, sym, off, n)
}

// stringRest writes the words of a string after its pointer, at off in
// sym, for a length of n: n (types.StringTwoWords), else the hash slot
// (zero) and n, or with types.StringWasm32 n, then zero for the hash
// and the last word.
func stringRest(gd *base.Invocation, sym *obj.LSym, off int, n uint64) int {
	if types.StringTwoWords() {
		// The length (a string not inline, even if short: see
		// types.StringTagged).
		return objw.Uintptr(gd, sym, off, n)
	}
	if types.StringWasm32() {
		off = objw.Uintptr(gd, sym, off, n)
		off = objw.Uintptr(gd, sym, off, 0)
		return objw.Uintptr(gd, sym, off, 0)
	}
	off = objw.Uintptr(gd, sym, off, 0) // hash slot
	return objw.Uintptr(gd, sym, off, n)
}
