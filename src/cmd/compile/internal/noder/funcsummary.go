// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"internal/buildcfg"
	"slices"
	"strconv"
	"strings"

	"cmd/compile/internal/ir"
	"cmd/internal/obj"
)

// What a package says of a function that it compiled, for the
// packages that call it without compiling it: its ABI, what escape
// analysis and the analyses of the fork found out about its parameters
// and results, and what it costs to inline. Export data has it for
// every function of the package (linker.relocFuncExt, reader.funcExt),
// and the archive's list of generic instantiations has it for each of
// those (gd/extinst.go), in text: one code writes both and one reads
// both, so that an instantiation that another package provides is all
// that an imported function is.

type summaryWriter interface {
	Bool(bool) bool
	Len(int)
	Uint64(uint64)
	String(string)
}

type summaryReader interface {
	Bool() bool
	Len() int
	Uint64() uint64
	String() string
}

func writeFuncSummary(w summaryWriter, name *ir.Name) {
	// Record definition ABI so cross-ABI calls can be direct.
	// This is important for the performance of calling some
	// common functions implemented in assembly (e.g., bytealg).
	w.Uint64(uint64(name.Func.ABI))

	// Escape analysis.
	for _, f := range name.Type().RecvParams() {
		w.String(f.Note)
	}
	w.Bool(name.Func.TakesLocalBits) // gd: see escape/callbits.go
	w.Len(len(name.Func.ParamShares))
	for _, ps := range name.Func.ParamShares {
		w.Len(int(ps.Share))
		if w.Bool(ps.ShareTyped) {
			w.Len(len(ps.ShareField))
			for _, i := range ps.ShareField {
				w.Len(int(i))
			}
		}
		w.Len(int(ps.Via))
		w.Len(int(ps.ViaDerefs))
		w.Bool(ps.ViaDirect)
		w.Len(int(ps.Results))
		w.Len(int(ps.Heap))
		w.Len(int(ps.Deep))
		w.Len(int(ps.DeepDerefs))
		w.Len(len(ps.MixLinks))
		for _, l := range ps.MixLinks {
			w.String(l.Key)
			w.Len(int(l.Arg))
			w.Len(int(l.Into))
			w.Bool(l.Bad)
		}
		w.Len(len(ps.MixGroups))
		for _, l := range ps.MixGroups {
			w.String(l.Key)
			w.Len(int(l.Arg))
			w.Len(int(l.Into))
			w.Bool(l.Bad)
		}
		w.Len(len(ps.StoreLinks))
		for _, l := range ps.StoreLinks {
			w.String(l.Key)
			w.Len(int(l.Arg))
		}
		w.Len(len(ps.Links))
		for _, l := range ps.Links {
			w.String(l.Key)
			w.Len(int(l.Arg))
			w.Len(int(l.Derefs))
		}
	}
	// The store types, as paths from the parameters' types (export
	// data cannot name a type here).
	for i, ps := range name.Func.ParamShares {
		params := name.Type().RecvParams()
		paths, ok := [][]uint8(nil), ps.StoreKnown && i < len(params)
		for _, t := range ps.StoreTypes {
			if !ok {
				break
			}
			var p []uint8
			p, ok = ir.TypePath(params[i].Type, t)
			paths = append(paths, p)
		}
		if w.Bool(ok) {
			slices.SortFunc(paths, slices.Compare)
			paths = slices.CompactFunc(paths, slices.Equal)
			w.Len(len(paths))
			for _, p := range paths {
				w.Len(len(p))
				for _, s := range p {
					w.Len(int(s))
				}
			}
		}
	}
	w.Len(int(name.Func.Consumes)) // gd: see escape/move.go
	w.Bool(name.Func.OwnedResult)
	w.Bool(name.Func.PoolResult)
	w.Len(int(name.Func.KeptChecked))
	w.Len(int(name.Func.KeptByRecv))
	w.Len(int(name.Func.KeptByAny))
	w.Len(int(name.Func.KeptCalls))
	w.Len(int(name.Func.DeepBits))
	w.Len(int(name.Func.DirtyInto))
	w.Bool(name.Func.ResultExact)
	w.Len(int(name.Func.ResultDeep))
	w.Len(int(name.Func.ResultFrom))
	for _, ls := range [][]ir.ShareLink{name.Func.ResultLinks, name.Func.ResultStores} {
		w.Len(len(ls))
		for _, l := range ls {
			w.String(l.Key)
			w.Len(int(l.Arg))
		}
	}
	w.Bool(name.Func.ShareOpaque)
	w.Len(int(name.Func.StoresInto))

	if inl := name.Func.Inl; w.Bool(inl != nil) {
		w.Len(int(inl.Cost))
		w.Bool(inl.CanDelayResults)
		if buildcfg.Experiment.NewInliner {
			w.String(inl.Properties)
		}
	}
}

func readFuncSummary(r summaryReader, name *ir.Name) {
	fn := name.Func
	fn.ABI = obj.ABI(r.Uint64())

	// Escape analysis. One note per RecvParam. If the
	// GdReturnOutBuf pragma was set above, RecvParams() already
	// has the outBufs appended — the writer wrote matching
	// N+outBufs notes. If the pragma is clear, RecvParams() is
	// stock and the writer wrote N notes.
	for _, f := range name.Type().RecvParams() {
		f.Note = r.String()
	}
	fn.TakesLocalBits = r.Bool()
	if n := r.Len(); n != 0 {
		fn.ParamShares = make([]ir.ParamShare, n)
		for i := range fn.ParamShares {
			ps := &fn.ParamShares[i]
			ps.Share = uint8(r.Len())
			if r.Bool() {
				ps.ShareTyped = true
				ps.ShareField = make([]uint8, r.Len())
				for i := range ps.ShareField {
					ps.ShareField[i] = uint8(r.Len())
				}
			}
			ps.Via = uint16(r.Len())
			ps.ViaDerefs = uint8(r.Len())
			ps.ViaDirect = r.Bool()
			ps.Results = uint8(r.Len())
			ps.Heap = uint8(r.Len())
			ps.Deep = uint16(r.Len())
			ps.DeepDerefs = uint8(r.Len())
			if n := r.Len(); n != 0 {
				ps.MixLinks = make([]ir.MixLink, n)
				for j := range ps.MixLinks {
					ps.MixLinks[j] = ir.MixLink{Key: r.String(), Arg: uint8(r.Len()), Into: uint16(r.Len()), Bad: r.Bool()}
				}
			}
			if n := r.Len(); n != 0 {
				ps.MixGroups = make([]ir.MixLink, n)
				for j := range ps.MixGroups {
					ps.MixGroups[j] = ir.MixLink{Key: r.String(), Arg: uint8(r.Len()), Into: uint16(r.Len()), Bad: r.Bool()}
				}
			}
			if n := r.Len(); n != 0 {
				ps.StoreLinks = make([]ir.ShareLink, n)
				for j := range ps.StoreLinks {
					ps.StoreLinks[j] = ir.ShareLink{Key: r.String(), Arg: uint8(r.Len())}
				}
			}
			if n := r.Len(); n != 0 {
				ps.Links = make([]ir.ShareLink, n)
				for j := range ps.Links {
					ps.Links[j] = ir.ShareLink{Key: r.String(), Arg: uint8(r.Len()), Derefs: uint8(r.Len())}
				}
			}
		}
		params := name.Type().RecvParams()
		for i := range fn.ParamShares {
			ps := &fn.ParamShares[i]
			if !r.Bool() {
				continue
			}
			ok := i < len(params)
			n := r.Len()
			for j := 0; j < n; j++ {
				p := make([]uint8, r.Len())
				for k := range p {
					p[k] = uint8(r.Len())
				}
				if !ok {
					continue
				}
				t := ir.TypeAtPath(params[i].Type, p)
				if t == nil {
					ok = false
					continue
				}
				ps.StoreTypes = append(ps.StoreTypes, t)
			}
			ps.StoreKnown = ok
			if !ok {
				ps.StoreTypes = nil
			}
		}
	}
	fn.Consumes = uint16(r.Len())
	fn.OwnedResult = r.Bool()
	fn.PoolResult = r.Bool()
	fn.KeptChecked = uint16(r.Len())
	fn.KeptByRecv = uint16(r.Len())
	fn.KeptByAny = uint16(r.Len())
	fn.KeptCalls = uint16(r.Len())
	fn.DeepBits = uint16(r.Len())
	fn.DirtyInto = uint16(r.Len())
	fn.ResultExact = r.Bool()
	fn.ResultDeep = uint16(r.Len())
	fn.ResultFrom = uint16(r.Len())
	for _, ls := range []*[]ir.ShareLink{&fn.ResultLinks, &fn.ResultStores} {
		if n := r.Len(); n != 0 {
			*ls = make([]ir.ShareLink, n)
			for j := range *ls {
				(*ls)[j] = ir.ShareLink{Key: r.String(), Arg: uint8(r.Len())}
			}
		}
	}
	fn.ShareOpaque = r.Bool()
	fn.StoresInto = uint16(r.Len())

	if r.Bool() {
		fn.Inl = &ir.Inline{
			Cost:            int32(r.Len()),
			CanDelayResults: r.Bool(),
		}
		if buildcfg.Experiment.NewInliner {
			fn.Inl.Properties = r.String()
		}
	}
}

// textSummary writes a summary as words separated by spaces: numbers,
// and strings, which start with an s and have %XX for every byte that
// is not a letter, a digit or one of a few signs. (No word has a space,
// a quote or a backslash: a line of them is a line of text to
// anything that looks for one in the archive.)
type textSummary struct {
	b strings.Builder
	n int
}

func (w *textSummary) word(s string) {
	if w.n != 0 {
		w.b.WriteByte(' ')
	}
	w.b.WriteString(s)
	w.n++
}
func (w *textSummary) Bool(b bool) bool {
	if b {
		w.word("1")
	} else {
		w.word("0")
	}
	return b
}
func (w *textSummary) Len(x int)       { w.word(strconv.Itoa(x)) }
func (w *textSummary) Uint64(x uint64) { w.word(strconv.FormatUint(x, 10)) }
func (w *textSummary) String(s string) {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.WriteByte('s')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("_.:/-[]*(),{}", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	w.word(b.String())
}

// summaryText is a summary that was read from text: its words, which
// are read in the order that they were written in. bad is set by a
// word that is not what is read there.
type summaryText struct {
	words []string
	bad   bool
}

func (r *summaryText) next() string {
	if len(r.words) == 0 {
		r.bad = true
		return ""
	}
	w := r.words[0]
	r.words = r.words[1:]
	return w
}
func (r *summaryText) Bool() bool { return r.Len() != 0 }
func (r *summaryText) Len() int {
	n, err := strconv.Atoi(r.next())
	if err != nil || n < 0 || n > 1<<20 {
		r.bad = true
		return 0
	}
	return n
}
func (r *summaryText) Uint64() uint64 {
	n, err := strconv.ParseUint(r.next(), 10, 64)
	if err != nil {
		r.bad = true
	}
	return n
}
func (r *summaryText) String() string {
	w := r.next()
	if w == "" || w[0] != 's' {
		r.bad = true
		return ""
	}
	w = w[1:]
	if strings.IndexByte(w, '%') < 0 {
		return w
	}
	var b strings.Builder
	for i := 0; i < len(w); i++ {
		if w[i] != '%' {
			b.WriteByte(w[i])
			continue
		}
		if i+2 >= len(w) {
			r.bad = true
			return ""
		}
		c, err := strconv.ParseUint(w[i+1:i+3], 16, 8)
		if err != nil {
			r.bad = true
			return ""
		}
		b.WriteByte(byte(c))
		i += 2
	}
	return b.String()
}

// splitSummary returns the words of a summary's text.
func splitSummary(line string) []string {
	if line == "" {
		return nil
	}
	return strings.Split(line, " ")
}
