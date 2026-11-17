// Package ccap is the run-time support of C compiled by cfront: pointers
// with derived capabilities (doc/gd/cfront.md).
//
// In a register (a Go variable) a C pointer is a Ptr: the base and size of
// the object it was derived from (its capability) and an offset, which may
// be anything. In memory a C pointer is one word, laid out as Go lays out a
// pointer, in a word the collector reads as a pointer:
//
//   - if the address is inside its object, the word is the address: a Go
//     pointer, which Go code can use as it is;
//   - otherwise the word is the address with bit 55 set (tagged): not
//     canonical, so it faults where Go dereferences it, and the collector
//     does not take it for a pointer. Its capability is kept in a side table
//     (aux), so C can bring it back into bounds.
//
// When C loads an untagged word, or receives a pointer from Go, the
// capability is derived from the address: the heap knows the object that
// contains it. That is sound because an untagged pointer word always points
// into a live object: Go's invariant, which every store of C keeps.
//
// Every access through a Ptr is checked against its capability. A store of
// a scalar may not write a word the collector reads as a pointer, and a
// store of a pointer may not write a word it does not.
package ccap

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

//go:linkname object runtime.ccapObject
func object(p uintptr) (base, size uintptr)

//go:linkname isPtr runtime.ccapIsPtr
func isPtr(addr uintptr) bool

// Tag marks a pointer word whose address is outside its object.
const Tag = 1 << 55

// Ptr is a C pointer in a register.
type Ptr struct {
	obj  unsafe.Pointer // base of the object, nil: no capability
	size uintptr        // size of the object
	off  int            // address - base; with no capability, the address
}

// A Fault is what a C access that its capability does not allow panics with.
type Fault struct {
	Msg  string
	Addr uintptr
}

func (f *Fault) Error() string { return fmt.Sprintf("ccap: %s (address %#x)", f.Msg, f.Addr) }

func fault(msg string, addr uintptr) { panic(&Fault{msg, addr}) }

// Addr returns the address p holds.
func Addr(p Ptr) uintptr { return uintptr(p.obj) + uintptr(p.off) }

// FromInt returns a pointer made of an integer: it has no capability.
func FromInt(u uintptr) Ptr { return Ptr{off: int(u)} }

// Add returns p moved by n bytes. Arithmetic is never checked.
func Add(p Ptr, n int) Ptr { p.off += n; return p }

// Sub returns the difference of the addresses of p and q.
func Sub(p, q Ptr) int { return int(Addr(p) - Addr(q)) }

// Cap describes p's capability, for tests and messages.
func Cap(p Ptr) string {
	if p.obj == nil {
		return fmt.Sprintf("{addr %#x, no capability}", p.off)
	}
	return fmt.Sprintf("{base %#x, size %d, off %d}", uintptr(p.obj), p.size, p.off)
}

// derive returns the pointer for the untagged word w.
func derive(w unsafe.Pointer) Ptr {
	if w == nil {
		return Ptr{}
	}
	base, size := object(uintptr(w))
	if base == 0 {
		// Not in the heap (a Go global, or memory C cannot know the
		// bounds of): no capability.
		return Ptr{off: int(uintptr(w))}
	}
	return Ptr{obj: unsafe.Pointer(base), size: size, off: int(uintptr(w) - base)}
}

var sink unsafe.Pointer
var never bool

// FromGo returns the C pointer for a Go pointer, deriving its capability.
// Its argument leaks, so that what Go passes is in the heap.
func FromGo(p unsafe.Pointer) Ptr {
	if never {
		sink = p
	}
	return derive(p)
}

// encode returns the word that represents p in memory.
func encode(p Ptr) (w unsafe.Pointer, tagged bool) {
	if p.obj != nil && p.off >= 0 && uintptr(p.off) < p.size {
		return unsafe.Add(p.obj, p.off), false
	}
	if p.obj == nil && p.off == 0 {
		return nil, false
	}
	return unsafe.Pointer(Addr(p) | Tag), true
}

// ToGo returns the Go pointer for p: its address if it is inside its
// object, a tagged word that faults when used if not.
func ToGo(p Ptr) unsafe.Pointer {
	w, _ := encode(p)
	return w
}

// The side table of the capabilities of tagged words, by the address of
// the word. A prototype: entries are not removed when the word's object
// dies, and what they point to stays alive.
var (
	auxMu sync.Mutex
	aux   = map[uintptr]auxEntry{}
	auxN  atomic.Int64
)

type auxEntry struct {
	word uintptr
	obj  unsafe.Pointer
	size uintptr
}

func check(p Ptr, n uintptr) unsafe.Pointer {
	if p.obj == nil {
		fault("access through a pointer with no capability", Addr(p))
	}
	if p.off < 0 || uintptr(p.off)+n > p.size {
		fault(fmt.Sprintf("access of %d bytes out of bounds of %s", n, Cap(p)), Addr(p))
	}
	return unsafe.Add(p.obj, p.off)
}

func checkPtrWord(p Ptr) unsafe.Pointer {
	a := check(p, 8)
	if uintptr(a)%8 != 0 {
		fault("misaligned pointer access", uintptr(a))
	}
	return a
}

// checkScalar checks a store of n bytes at p, which may not write a word
// that the collector reads as a pointer.
func checkScalar(p Ptr, n uintptr) unsafe.Pointer {
	a := check(p, n)
	for w := uintptr(a) &^ 7; w < uintptr(a)+n; w += 8 {
		if isPtr(w) {
			fault("scalar store over a pointer word", uintptr(a))
		}
	}
	return a
}

// LoadPtr loads the pointer at p.
func LoadPtr(p Ptr) Ptr {
	a := checkPtrWord(p)
	if !isPtr(uintptr(a)) {
		// An integer, read as a pointer: no capability.
		return FromInt(*(*uintptr)(a))
	}
	w := *(*unsafe.Pointer)(a)
	if uintptr(w)&Tag == 0 {
		return derive(w)
	}
	addr := uintptr(w) &^ Tag
	auxMu.Lock()
	e, ok := aux[uintptr(a)]
	auxMu.Unlock()
	if !ok || e.word != uintptr(w) {
		// Copied or written by Go: the capability is lost.
		return FromInt(addr)
	}
	return Ptr{obj: e.obj, size: e.size, off: int(addr - uintptr(e.obj))}
}

// StorePtr stores the pointer v at p.
func StorePtr(p, v Ptr) {
	a := checkPtrWord(p)
	if !isPtr(uintptr(a)) {
		fault("pointer store into a word the collector does not scan", uintptr(a))
	}
	w, tagged := encode(v)
	if tagged && v.obj != nil {
		auxMu.Lock()
		if _, ok := aux[uintptr(a)]; !ok {
			auxN.Add(1)
		}
		aux[uintptr(a)] = auxEntry{uintptr(w), v.obj, v.size}
		auxMu.Unlock()
	} else if auxN.Load() > 0 {
		auxMu.Lock()
		if _, ok := aux[uintptr(a)]; ok {
			delete(aux, uintptr(a))
			auxN.Add(-1)
		}
		auxMu.Unlock()
	}
	*(*unsafe.Pointer)(a) = w // with the write barrier
}

func Load8(p Ptr) uint8   { return *(*uint8)(check(p, 1)) }
func Load16(p Ptr) uint16 { return *(*uint16)(check(p, 2)) }
func Load32(p Ptr) uint32 { return *(*uint32)(check(p, 4)) }
func Load64(p Ptr) uint64 { return *(*uint64)(check(p, 8)) }

func LoadF32(p Ptr) float32 { return math.Float32frombits(Load32(p)) }
func LoadF64(p Ptr) float64 { return math.Float64frombits(Load64(p)) }

func Store8(p Ptr, v uint8)   { *(*uint8)(checkScalar(p, 1)) = v }
func Store16(p Ptr, v uint16) { *(*uint16)(checkScalar(p, 2)) = v }
func Store32(p Ptr, v uint32) { *(*uint32)(checkScalar(p, 4)) = v }
func Store64(p Ptr, v uint64) { *(*uint64)(checkScalar(p, 8)) = v }

func StoreF32(p Ptr, v float32) { Store32(p, math.Float32bits(v)) }
func StoreF64(p Ptr, v float64) { Store64(p, math.Float64bits(v)) }

// New allocates a T, typed: the collector knows its pointer words.
func New[T any]() Ptr {
	p := new(T)
	return Ptr{obj: unsafe.Pointer(p), size: unsafe.Sizeof(*p)}
}

// NewArray allocates n bytes as an array of T.
func NewArray[T any](n uintptr) Ptr {
	var z T
	sz := unsafe.Sizeof(z)
	if sz == 0 {
		sz = 1
	}
	count := (n + sz - 1) / sz
	s := make([]T, max(count, 1))
	return Ptr{obj: unsafe.Pointer(&s[0]), size: count * unsafe.Sizeof(z)}
}

// Malloc allocates n bytes with no pointer words.
func Malloc(n uintptr) Ptr { return NewArray[byte](n) }

// Free does nothing: the collector frees what is not reachable.
func Free(p Ptr) {}

// Memmove copies n bytes from src to dst. A word that is a pointer in
// both is copied as a pointer (with its capability); a pointer copied into
// a scalar word becomes its address; a scalar copied into a pointer word
// must be zero.
func Memmove(dst, src Ptr, n uintptr) {
	if n == 0 {
		return
	}
	d := check(dst, n)
	s := check(src, n)
	type word struct {
		ptr bool
		p   Ptr
		u   uint64
	}
	if uintptr(d)%8 == 0 && uintptr(s)%8 == 0 {
		nw := n / 8
		ws := make([]word, nw)
		for i := range nw {
			sp := Add(src, int(i*8))
			if isPtr(uintptr(s) + i*8) {
				ws[i] = word{ptr: true, p: LoadPtr(sp)}
			} else {
				ws[i] = word{u: Load64(sp)}
			}
		}
		tail := make([]byte, n%8)
		copy(tail, unsafe.Slice((*byte)(unsafe.Add(s, nw*8)), n%8))
		for i, w := range ws {
			dp := Add(dst, i*8)
			switch {
			case isPtr(uintptr(d) + uintptr(i)*8):
				if !w.ptr {
					if w.u != 0 {
						fault("non-zero scalar copied into a pointer word", uintptr(d)+uintptr(i)*8)
					}
					StorePtr(dp, Ptr{})
				} else {
					StorePtr(dp, w.p)
				}
			case w.ptr:
				Store64(dp, uint64(Addr(w.p)))
			default:
				Store64(dp, w.u)
			}
		}
		if len(tail) > 0 {
			t := checkScalar(Add(dst, int(nw*8)), uintptr(len(tail)))
			copy(unsafe.Slice((*byte)(t), len(tail)), tail)
		}
		return
	}
	checkScalar(dst, n)
	copy(unsafe.Slice((*byte)(d), n), unsafe.Slice((*byte)(s), n))
}

// Memset sets n bytes at p to c. Pointer words may only be set to zero.
func Memset(p Ptr, c int32, n uintptr) Ptr {
	a := check(p, n)
	for i := uintptr(0); i < n; {
		at := uintptr(a) + i
		if at%8 == 0 && i+8 <= n && isPtr(at) {
			if c&0xff != 0 {
				fault("non-zero memset of a pointer word", at)
			}
			StorePtr(Add(p, int(i)), Ptr{})
			i += 8
			continue
		}
		Store8(Add(p, int(i)), uint8(c))
		i++
	}
	return p
}

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
