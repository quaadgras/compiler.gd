// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package getopt

import (
	c23 "internal/c23"
)

// GetoptLong is getopt_long: getopt, and the long options --name,
// --name=value and --name value (or abbreviations of name that are not
// ambiguous) of longopts.
func GetoptLong(argc int32, argv, optstring, longopts, longind *c23.Void) int32 {
	return c23.Getopt(argc, argv, optstring, longopts, longind, false)
}

// GetoptLongOnly is getopt_long_only: getopt_long, where -name is a long
// option too, unless it is a short option of optstring.
func GetoptLongOnly(argc int32, argv, optstring, longopts, longind *c23.Void) int32 {
	return c23.Getopt(argc, argv, optstring, longopts, longind, true)
}
