// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fatal

import (
	"cmd/internal/src"
	"fmt"
	"internal/types/errors"
)

func Error(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}

func ErrorAt(pos src.XPos, code errors.Code, format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}
