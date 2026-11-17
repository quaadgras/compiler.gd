// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import (
	"fmt"
	"strings"
	"sync"
)

// libc maps the functions of the headers cmd/cc provides to their Go.
var libc = map[string]string{
	"printf": "Printf", "fprintf": "Fprintf", "sprintf": "Sprintf", "snprintf": "Snprintf",
	"vprintf": "Vprintf", "vfprintf": "Vfprintf", "vsprintf": "Vsprintf", "vsnprintf": "Vsnprintf",
	"puts": "Puts", "fputs": "Fputs", "putchar": "Putchar", "fputc": "Fputc", "putc": "Fputc",
	"getchar": "Getchar", "fgetc": "Fgetc", "getc": "Fgetc", "fgets": "Fgets", "fwrite": "Fwrite",
	"fread": "Fread", "fflush": "Fflush",
	"malloc": "Malloc", "calloc": "Calloc", "realloc": "Realloc", "free": "Free",
	"abort": "Abort", "exit": "Exit", "_Exit": "Exit", "abs": "Abs", "labs": "Labs", "llabs": "Labs",
	"atoi": "Atoi", "atol": "Atol", "atoll": "Atol", "strtol": "Strtol", "strtoul": "Strtoul",
	"strtoll": "Strtol", "strtoull": "Strtoul", "strtod": "Strtod", "atof": "Atof",
	"rand": "Rand", "srand": "Srand", "getenv": "Getenv",
	"memcpy": "Memcpy", "memmove": "Memcpy", "memset": "Memset", "memcmp": "Memcmp", "memchr": "Memchr",
	"memccpy": "Memccpy",
	"strcpy":  "Strcpy", "strncpy": "Strncpy", "strcat": "Strcat", "strncat": "Strncat",
	"strcmp": "Strcmp", "strncmp": "Strncmp", "strlen": "Strlen", "strchr": "Strchr",
	"strrchr": "Strrchr", "strstr": "Strstr", "strdup": "Strdup", "strndup": "Strndup",
	"strspn": "Strspn", "strcspn": "Strcspn", "__cc_stdio": "Stdio",
}

var (
	libcTypesOnce sync.Once
	libcTypesMap  map[string]*ctype
)

// libcType is the type of a library function, as its header declares it.
func libcType(name string) *ctype {
	libcTypesOnce.Do(func() {
		libcTypesMap = map[string]*ctype{}
		var src strings.Builder
		for _, h := range Headers() {
			fmt.Fprintf(&src, "#include <%s>\n", h)
		}
		toks, err := lexUnit([]source{{"<headers>", src.String()}})
		if err != nil {
			panic(err)
		}
		u, err := parse("<headers>", toks)
		if err != nil {
			panic(err)
		}
		for _, s := range u.syms {
			if s.kind == symFunc {
				libcTypesMap[s.name] = s.t
			}
		}
	})
	return libcTypesMap[name]
}
