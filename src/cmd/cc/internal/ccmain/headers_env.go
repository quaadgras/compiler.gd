// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// The environment as C has it: environ, a variable of the library's.

func init() {
	extendHeader("unistd.h", "extern char **environ;\n")
	extendHeader("stdlib.h", "int putenv(char *string);\nint clearenv(void);\n")
	libc["putenv"] = "stdlib.Putenv"
	libc["clearenv"] = "stdlib.Clearenv"
}

// libcVars are the library's variables: an extern declaration of one
// that the program does not define is the library's object, whose
// address the Go function of its value returns (as libc's values name
// functions).
var libcVars = map[string]string{
	"environ":   "stdlib.Environ",
	"__environ": "stdlib.Environ",
}
