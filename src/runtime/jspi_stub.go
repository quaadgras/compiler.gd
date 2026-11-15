// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(js && wasm.jspi)

package runtime

// See jspi_js.go.
const jspi = false

const jsCallerDepth = 0

var jsgdead uintptr

func jsthrowgoexit()     { throw("jspi") }
func jsthrowrecover()    { throw("jspi") }
func jsreturn()          { throw("jspi") }
func jsidle()            {}
func jsgoexit(p *_panic) { throw("jspi") }
