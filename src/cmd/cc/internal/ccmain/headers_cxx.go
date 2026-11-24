// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The C++ standard library's headers that cmd/cc has: written for it,
// with no library behind them (doc/gd/cxx.md). A <cname> header is C's
// <name.h>: std's names that are C's are found in the global namespace
// (scope.find).

func init() {
	for _, name := range []string{"assert", "ctype", "errno", "fenv", "float", "inttypes", "limits", "locale",
		"math", "setjmp", "signal", "stdarg", "stddef", "stdint", "stdio", "stdlib", "string", "time", "uchar",
		"wchar", "wctype"} {
		guard := "__CC_C" + name
		sysHeaders["c"+name] = "#ifndef " + guard + "\n#define " + guard + "\n#include <" + name + ".h>\nnamespace std {}\n#endif\n"
	}
	sysHeaders["new"] = `#ifndef __CC_NEW
#define __CC_NEW
#include <stddef.h>
namespace std {
struct nothrow_t {};
enum class align_val_t : size_t {};
}
inline void *operator new(size_t, void *__p) noexcept { return __p; }
inline void *operator new[](size_t, void *__p) noexcept { return __p; }
inline void operator delete(void *, void *) noexcept {}
inline void operator delete[](void *, void *) noexcept {}
#endif
`
}
