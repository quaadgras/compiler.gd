// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package c23

import (
	"fmt"
	"os"
	"strings"
	"unsafe"
)

// CString allocates s as a C string.
func CString(s string) Ptr {
	p := Malloc(uintptr(len(s) + 1))
	copy(unsafe.Slice((*byte)(p.obj), len(s)), s)
	return p
}

// GoString reads the C string at p.
func GoString(p Ptr) string {
	var b strings.Builder
	for {
		c := Load8(p)
		if c == 0 {
			return b.String()
		}
		b.WriteByte(c)
		p = Add(p, 1)
	}
}

func Strlen(p Ptr) uint64 { return uint64(len(GoString(p))) }

func Strcmp(a, b Ptr) int32 {
	for {
		x, y := Load8(a), Load8(b)
		if x != y || x == 0 {
			return int32(x) - int32(y)
		}
		a, b = Add(a, 1), Add(b, 1)
	}
}

func Strcpy(d, s Ptr) Ptr {
	r := d
	for {
		c := Load8(s)
		Store8(d, c)
		if c == 0 {
			return r
		}
		d, s = Add(d, 1), Add(s, 1)
	}
}

func B2I(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func Puts(p Ptr) int32 { fmt.Println(GoString(p)); return 0 }

func Putchar(c int32) int32 { os.Stdout.Write([]byte{byte(c)}); return c }

func Abort() { panic(&Fault{"abort", 0}) }

func Assert(ok bool, what string) {
	if !ok {
		panic(&Fault{"assertion failed: " + what, 0})
	}
}

// Printf formats as C's printf, for the conversions d i u x X c s p f g e
// and %%, with the flags and widths fmt shares with C.
func Printf(format Ptr, args ...any) int32 {
	s := Sprintf(GoString(format), args...)
	os.Stdout.WriteString(s)
	return int32(len(s))
}

func Sprintf(f string, args ...any) string {
	var out strings.Builder
	ai := 0
	next := func() any {
		if ai >= len(args) {
			return 0
		}
		a := args[ai]
		ai++
		return a
	}
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			out.WriteByte(f[i])
			continue
		}
		j := i + 1
		for j < len(f) && strings.IndexByte("-+ #0123456789.", f[j]) >= 0 {
			j++
		}
		spec := f[i+1 : j]
		for j < len(f) && strings.IndexByte("hlzjt", f[j]) >= 0 {
			j++
		}
		if j >= len(f) {
			break
		}
		conv := f[j]
		i = j
		switch conv {
		case '%':
			out.WriteByte('%')
		case 'd', 'i':
			fmt.Fprintf(&out, "%"+spec+"d", toInt(next()))
		case 'u':
			fmt.Fprintf(&out, "%"+spec+"d", toUint(next()))
		case 'x', 'X', 'o':
			fmt.Fprintf(&out, "%"+spec+string(conv), toUint(next()))
		case 'c':
			out.WriteByte(byte(toInt(next())))
		case 's':
			p, _ := next().(Ptr)
			fmt.Fprintf(&out, "%"+spec+"s", GoString(p))
		case 'p':
			p, _ := next().(Ptr)
			fmt.Fprintf(&out, "%#x", Addr(p))
		case 'f', 'g', 'e', 'F', 'G', 'E':
			v, _ := next().(float64)
			fmt.Fprintf(&out, "%"+spec+string(conv), v)
		}
	}
	return out.String()
}

func toInt(a any) int64 {
	switch v := a.(type) {
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(int32(v))
	case uint64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}

func toUint(a any) uint64 {
	switch v := a.(type) {
	case int8:
		return uint64(uint8(v))
	case int16:
		return uint64(uint16(v))
	case int32:
		return uint64(uint32(v))
	case uint32:
		return uint64(v)
	}
	return uint64(toInt(a))
}

// Memcpy is C's memcpy and memmove.
func Memcpy(dst, src Ptr, n uintptr) Ptr {
	Memmove(dst, src, n)
	return dst
}

// Realloc allocates n bytes as an array of T and copies what p's object
// holds from p on.
func Realloc[T any](p Ptr, n uintptr) Ptr {
	q := NewArray[T](n)
	if p.obj != nil {
		Memmove(q, p, min(n, p.size-uintptr(p.off)))
	}
	return q
}
