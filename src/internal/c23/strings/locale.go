// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import c23 "internal/c23"

// The functions of a locale (POSIX's _l functions), which are the global
// locale's: the library's locales compare alike.

func StrcasecmpL(a, b, _ *c23.Void) int32 { return Strcasecmp(a, b) }

func StrncasecmpL(a, b *c23.Void, n uint64, _ *c23.Void) int32 { return Strncasecmp(a, b, n) }
