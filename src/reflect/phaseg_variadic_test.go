// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect_test

import (
	. "reflect"
	"testing"
)

// gd Phase G: on a variadic sig the synthesised outBuf params sit
// BEFORE the trailing ...T param (types.NewSignature keeps the
// variadic slice last), so every reflect surface that maps between
// the user-visible and raw param views must skip outBufs by
// position rather than assuming a trailing block. These tests lock
// down that mapping for a variadic pointer-returning function; see
// FuncType.OutBufStart.

type phaseGVariadicRecv struct{}

func (phaseGVariadicRecv) Join(sep string, parts ...string) *string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += sep
		}
		s += p
	}
	return &s
}

func phaseGVariadic(a int, rest ...string) *int {
	x := a + len(rest)
	return &x
}

func TestPhaseGVariadicType(t *testing.T) {
	ft := TypeOf(phaseGVariadic)
	if got := ft.NumIn(); got != 2 {
		t.Fatalf("NumIn = %d, want 2", got)
	}
	if got := ft.In(0); got != TypeOf(int(0)) {
		t.Errorf("In(0) = %v, want int", got)
	}
	if got := ft.In(1); got != TypeOf([]string(nil)) {
		t.Errorf("In(1) = %v, want []string (a synthesised outBuf leaked into the user view)", got)
	}
	if !ft.IsVariadic() {
		t.Errorf("IsVariadic = false, want true")
	}
}

func TestPhaseGVariadicCall(t *testing.T) {
	v := ValueOf(phaseGVariadic)
	out := v.Call([]Value{ValueOf(3), ValueOf("a"), ValueOf("b")})
	if got := *out[0].Interface().(*int); got != 5 {
		t.Errorf("Call = %d, want 5", got)
	}
	out = v.CallSlice([]Value{ValueOf(3), ValueOf([]string{"x"})})
	if got := *out[0].Interface().(*int); got != 4 {
		t.Errorf("CallSlice = %d, want 4", got)
	}
}

func TestPhaseGVariadicMakeFunc(t *testing.T) {
	ft := TypeOf(phaseGVariadic)
	mk := MakeFunc(ft, func(args []Value) []Value {
		if len(args) != 2 {
			t.Fatalf("MakeFunc callback got %d args, want 2 (outBufs must be stripped)", len(args))
		}
		if args[1].Kind() != Slice {
			t.Fatalf("MakeFunc callback arg 1 kind = %v, want slice", args[1].Kind())
		}
		n := args[0].Interface().(int) + args[1].Len()
		return []Value{ValueOf(&n)}
	})
	g := mk.Interface().(func(int, ...string) *int)
	if got := *g(1, "a", "b", "c"); got != 4 {
		t.Errorf("MakeFunc direct call = %d, want 4", got)
	}
	out := mk.Call([]Value{ValueOf(2), ValueOf("z")})
	if got := *out[0].Interface().(*int); got != 3 {
		t.Errorf("MakeFunc reflect Call = %d, want 3", got)
	}
}

func TestPhaseGVariadicMethod(t *testing.T) {
	m, ok := TypeOf(phaseGVariadicRecv{}).MethodByName("Join")
	if !ok {
		t.Fatal("Join method not found")
	}
	if got := m.Type.NumIn(); got != 3 {
		t.Fatalf("method NumIn = %d, want 3", got)
	}
	if got := m.Type.In(1); got != TypeOf("") {
		t.Errorf("method In(1) = %v, want string", got)
	}
	if got := m.Type.In(2); got != TypeOf([]string(nil)) {
		t.Errorf("method In(2) = %v, want []string", got)
	}
	out := m.Func.Call([]Value{ValueOf(phaseGVariadicRecv{}), ValueOf("-"), ValueOf("a"), ValueOf("b")})
	if got := *out[0].Interface().(*string); got != "a-b" {
		t.Errorf("method Func.Call = %q, want \"a-b\"", got)
	}
	vm := ValueOf(phaseGVariadicRecv{}).MethodByName("Join")
	out = vm.Call([]Value{ValueOf("+"), ValueOf("x"), ValueOf("y")})
	if got := *out[0].Interface().(*string); got != "x+y" {
		t.Errorf("method value Call = %q, want \"x+y\"", got)
	}
}
