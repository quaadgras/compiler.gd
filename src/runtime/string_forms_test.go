// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"unique"
	"unsafe"
)

// A string of 1 to 15 bytes made at run time is inline: its bytes are
// in its two words (doc/gd/sso-string.md). A constant is not: its bytes
// are in rodata. The tests below check that the two forms of a value
// are the same string.

//go:noinline
func runtimeString(s string) string {
	return string([]byte(s)) // made at run time: inline if short
}

func stringInline(s string) bool {
	w0 := *(*uintptr)(unsafe.Pointer(&s))
	return unsafe.Sizeof(w0) == 8 && unsafe.Sizeof(s) == 16 && uint64(w0)>>63 != 0
}

func TestStringForms(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof("") == 16 {
		if stringInline("abc") {
			t.Errorf("constant \"abc\" is inline")
		}
		if !stringInline(runtimeString("abc")) {
			t.Errorf("run-time \"abc\" is not inline")
		}
	}
	var consts = []string{"", "a", "ab", "hello", "1234567", "12345678", "123456789", "fifteen-bytes!!", "sixteen-bytes!!!", "a longer constant string"}
	for _, c := range consts {
		r := runtimeString(c)
		var ac, ar any = c, r
		switch {
		case c != r, !(c == r), r != c:
			t.Errorf("%q: == between forms", c)
		case c < r, r < c, !(c <= r) || !(r >= c):
			t.Errorf("%q: < between forms", c)
		case strings.Compare(c, r) != 0, strings.Compare(r, c) != 0:
			t.Errorf("%q: strings.Compare between forms", c)
		case ac != ar:
			t.Errorf("%q: interface == between forms", c)
		case len(r) != len(c) || r[:len(r)/2] != c[:len(c)/2]:
			t.Errorf("%q: len or substring between forms", c)
		}
		if c == "" {
			continue
		}
		// One longer and one shorter, of each form.
		r1 := runtimeString(c + "x")
		if c == r1 || r1 == c || c+"x" != r1 || !(c < r1) || strings.Compare(r1, c) != 1 {
			t.Errorf("%q: compare with %q", c, r1)
		}
		m := map[string]int{c: 1}
		if m[r] != 1 {
			t.Errorf("%q: map key, constant stored, run-time looked up", c)
		}
		m2 := map[string]int{r: 2}
		if m2[c] != 2 {
			t.Errorf("%q: map key, run-time stored, constant looked up", c)
		}
		m2[c] = 3
		if len(m2) != 1 || m2[r] != 3 {
			t.Errorf("%q: map has %d keys after storing both forms", c, len(m2))
		}
		if unique.Make(c) != unique.Make(r) {
			t.Errorf("%q: unique.Make between forms", c)
		}
	}

	// switch on constants with run-time values.
	for i, c := range consts {
		var got int
		switch runtimeString(c) {
		case "":
			got = 0
		case "a":
			got = 1
		case "ab":
			got = 2
		case "hello":
			got = 3
		case "1234567":
			got = 4
		case "12345678":
			got = 5
		case "123456789":
			got = 6
		case "fifteen-bytes!!":
			got = 7
		case "sixteen-bytes!!!":
			got = 8
		case "a longer constant string":
			got = 9
		default:
			got = -1
		}
		if got != i {
			t.Errorf("switch %q: case %d, want %d", c, got, i)
		}
	}

	// sort a mix of the forms.
	var mix []string
	for i := 0; i < 200; i++ {
		s := strconv.Itoa(i % 50)
		if i%2 == 0 {
			s = runtimeString(s)
		}
		mix = append(mix, s, consts[i%len(consts)])
	}
	slices.Sort(mix)
	if !slices.IsSorted(mix) {
		t.Errorf("mixed forms not sorted")
	}
	for i := 1; i < len(mix); i++ {
		if mix[i-1] > mix[i] {
			t.Fatalf("%q > %q after sort", mix[i-1], mix[i])
		}
	}
}
