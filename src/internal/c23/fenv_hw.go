// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 || arm64

package c23

// fpHardware: these architectures have rounding directions and exception
// flags, which the helpers in fenv_$GOARCH.s set and read.
const FpHardware = true

func FpAdd64(ctl uint64, x, y float64) (r float64, st uint64)
func FpSub64(ctl uint64, x, y float64) (r float64, st uint64)
func FpMul64(ctl uint64, x, y float64) (r float64, st uint64)
func FpDiv64(ctl uint64, x, y float64) (r float64, st uint64)
func fpAdd32(ctl uint64, x, y float32) (r float32, st uint64)
func fpSub32(ctl uint64, x, y float32) (r float32, st uint64)
func fpMul32(ctl uint64, x, y float32) (r float32, st uint64)
func fpDiv32(ctl uint64, x, y float32) (r float32, st uint64)
func FpSqrt64(ctl uint64, x float64) (r float64, st uint64)
func fpSqrt32(ctl uint64, x float32) (r float32, st uint64)
func FpNarrow(ctl uint64, x float64) (r float32, st uint64)
func fpIntTo64(ctl uint64, x int64) (r float64, st uint64)
func fpIntTo32(ctl uint64, x int64) (r float32, st uint64)
func FpFma64(ctl uint64, x, y, z float64) (r float64, st uint64)
func FpFma32(ctl uint64, x, y, z float32) (r float32, st uint64)
