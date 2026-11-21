// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 && !arm64

package c23

import "math"

// No rounding directions or exception flags (wasm has none): Go's
// arithmetic, rounding to nearest, raising nothing.
const (
	FpHardware = false
	FpHasFMA   = false
)

func FpControl(r int32) uint64  { return 0 }
func FpExcepts(st uint64) int32 { return 0 }

func FpAdd64(ctl uint64, x, y float64) (float64, uint64)    { return x + y, 0 }
func FpSub64(ctl uint64, x, y float64) (float64, uint64)    { return x - y, 0 }
func FpMul64(ctl uint64, x, y float64) (float64, uint64)    { return x * y, 0 }
func FpDiv64(ctl uint64, x, y float64) (float64, uint64)    { return x / y, 0 }
func fpAdd32(ctl uint64, x, y float32) (float32, uint64)    { return x + y, 0 }
func fpSub32(ctl uint64, x, y float32) (float32, uint64)    { return x - y, 0 }
func fpMul32(ctl uint64, x, y float32) (float32, uint64)    { return x * y, 0 }
func fpDiv32(ctl uint64, x, y float32) (float32, uint64)    { return x / y, 0 }
func FpSqrt64(ctl uint64, x float64) (float64, uint64)      { return math.Sqrt(x), 0 }
func fpSqrt32(ctl uint64, x float32) (float32, uint64)      { return float32(math.Sqrt(float64(x))), 0 }
func FpNarrow(ctl uint64, x float64) (float32, uint64)      { return float32(x), 0 }
func fpIntTo64(ctl uint64, x int64) (float64, uint64)       { return float64(x), 0 }
func fpIntTo32(ctl uint64, x int64) (float32, uint64)       { return float32(x), 0 }
func FpFma64(ctl uint64, x, y, z float64) (float64, uint64) { return math.FMA(x, y, z), 0 }
func FpFma32(ctl uint64, x, y, z float32) (float32, uint64) {
	return float32(math.FMA(float64(x), float64(y), float64(z))), 0
}
